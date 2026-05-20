// Package journal implements the per-session structured event log: an
// append-only JSONL file recording prompts, notes, checkpoints, and handoffs
// so multiple agents can audit and resume work across sessions.
//
// Journaling is best-effort. Callers log Open/Append errors as warnings and
// never fail a session because the journal could not be written.
package journal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SchemaVersion is stamped on every event as `v`. Bump on incompatible
// envelope or payload changes.
const SchemaVersion = 1

// Event types. Exactly one `header` per file; the rest may repeat.
const (
	TypeHeader       = "header"       // once per file: cs_version + envelope
	TypePrompt       = "prompt"       // a real user prompt, captured passively by an adapter
	TypeNote         = "note"         // a manual note
	TypeCheckpoint   = "checkpoint"   // user-triggered signed checkpoint
	TypeHandoff      = "handoff"      // user-triggered cross-agent handoff
	TypeIntent       = "intent"       // operator-curated statement of what the session is for
	TypeVerification = "verification" // verification result, status + freeform evidence
)

// Task-status vocabulary: the lifecycle state of a session as the operator
// sees it. Distinct from instance.Status (Running/Paused/...), which is the
// runtime state of the tmux pane.
const (
	TaskStatusActive         = "active"
	TaskStatusBlocked        = "blocked"
	TaskStatusAwaitingReview = "awaiting-review"
	TaskStatusFinished       = "finished"
	TaskStatusAbandoned      = "abandoned"
)

// Verification-status vocabulary. Borrowed verbatim from miagent so cross-tool
// audit tooling can read the same tokens. `not-run` always requires a reason.
const (
	VerificationStatusNotRun  = "not-run"
	VerificationStatusPartial = "partial"
	VerificationStatusPassed  = "passed"
	VerificationStatusFailed  = "failed"
	VerificationStatusNA      = "n/a"
)

// Final-disposition vocabulary, used by the (forthcoming) `finish` event to
// record how the session landed.
const (
	DispositionMerged    = "merged"
	DispositionAbandoned = "abandoned"
	DispositionHandedOff = "handed-off"
	DispositionOther     = "other"
)

// Agent names recorded in agent.name.
const (
	AgentClaudeCode = "claude-code"
	AgentCodex      = "codex"
	AgentAider      = "aider"
	AgentCS         = "cs"
	AgentHuman      = "human"
)

// SessionRef identifies the session an event belongs to. ID is a UUID minted
// once at first Start and never changed; Title may be renamed over time.
type SessionRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// AgentRef identifies the agent that produced an event. Version is the agent
// CLI's own version (e.g. the claude-code version), NOT claude-squad's —
// claude-squad's version lives once in the header event's CSVersion.
type AgentRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Signature is the checkpoint hash-chain entry embedded in checkpoint and
// handoff events. See ComputeSignature for the hashed input.
type Signature struct {
	Prev string `json:"prev"` // hex hash of the previous checkpoint, "" for the first
	Hash string `json:"hash"` // hex sha256 of this checkpoint
	From int64  `json:"from"` // byte offset in journal.jsonl where the signed range starts
	To   int64  `json:"to"`   // byte offset where the signed range ends (exclusive)
}

// Handoff is the structured payload of a handoff event, naming the current
// role after the transition, the queued next role, a lifecycle phase, and
// the concrete next action. The latest handoff in a journal is the session's
// current coordination state — append-only means the change history falls
// out for free without mutating earlier lines.
type Handoff struct {
	Role     string `json:"role,omitempty"`     // current owner AFTER this handoff
	Awaiting string `json:"awaiting,omitempty"` // queued next owner (empty = within-task)
	Phase    string `json:"phase,omitempty"`    // free-form lifecycle phase, e.g. awaiting-qa
	Next     string `json:"next,omitempty"`     // concrete next action for the queued owner
}

// Validate rejects an empty handoff — at least one state-changing field must
// be set, otherwise the event records nothing. Callers should run this
// before Append; the journal stays best-effort.
func (h Handoff) Validate() error {
	if h.Role == "" && h.Awaiting == "" && h.Phase == "" && h.Next == "" {
		return errors.New("handoff must set at least one of role, awaiting, phase, next")
	}
	return nil
}

// Verification is the structured payload of a verification event. Status is
// one of VerificationStatus*; Reason is required when Status is not-run.
// Evidence is freeform — command output, paths, notes — and is what readers
// actually look at to decide whether the verification is trustworthy.
type Verification struct {
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// Validate reports whether v has a recognized status and, when not-run, a
// reason. Callers should run this before Append so the journal never grows a
// malformed verification event; the journal itself stays best-effort.
func (v Verification) Validate() error {
	if !ValidVerificationStatus(v.Status) {
		return fmt.Errorf("invalid verification status %q", v.Status)
	}
	if v.Status == VerificationStatusNotRun && strings.TrimSpace(v.Reason) == "" {
		return errors.New("verification status not-run requires a reason")
	}
	return nil
}

// ValidVerificationStatus reports whether status is one of the allowed
// verification-status tokens. Empty is invalid.
func ValidVerificationStatus(status string) bool {
	switch status {
	case VerificationStatusNotRun,
		VerificationStatusPartial,
		VerificationStatusPassed,
		VerificationStatusFailed,
		VerificationStatusNA:
		return true
	}
	return false
}

// Event is one journal record. Every event carries the full envelope (V..Agent);
// payload fields are populated per Type and omitted otherwise.
type Event struct {
	V         int        `json:"v"`
	ID        string     `json:"id"`
	TS        time.Time  `json:"ts"`
	Type      string     `json:"type"`
	Session   SessionRef `json:"session"`
	Workspace string     `json:"workspace"`
	Agent     AgentRef   `json:"agent"`

	// CSVersion is set only on the header event: claude-squad's own version.
	CSVersion string `json:"cs_version,omitempty"`

	// Text carries the body of prompt and note events.
	Text string `json:"text,omitempty"`

	// Summary and Signature are set on checkpoint and handoff events.
	Summary   string     `json:"summary,omitempty"`
	GitSHA    string     `json:"git_sha,omitempty"`
	Signature *Signature `json:"signature,omitempty"`

	// Verification is set on verification events and (later) on finish
	// events that carry an inline verification block.
	Verification *Verification `json:"verification,omitempty"`

	// Handoff is set on handoff events: the role/awaiting/phase/next
	// payload concretized from miagent's task-record metadata.
	Handoff *Handoff `json:"handoff,omitempty"`
}

// NewID returns a roughly time-sortable event id: 10 hex chars of the current
// millisecond clock (low 40 bits) followed by 16 random hex chars.
func NewID() string {
	ms := time.Now().UnixMilli() & 0xFFFFFFFFFF // low 40 bits -> 10 hex chars
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("%010x%x", ms, rnd)
}

// NewSessionID returns a new random UUIDv4. Minted once per session at first
// Start; the title may later change but the id is the stable identity.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// HeaderEvent builds the once-per-file header event. csVersion is claude-squad's
// own version string.
func HeaderEvent(csVersion string) Event {
	return Event{Type: TypeHeader, Agent: AgentRef{Name: AgentCS}, CSVersion: csVersion}
}

// PromptEvent builds a prompt event captured passively from an agent adapter.
func PromptEvent(agent AgentRef, text string) Event {
	return Event{Type: TypePrompt, Agent: agent, Text: text}
}

// NoteEvent builds a manual note event.
func NoteEvent(agent AgentRef, text string) Event {
	return Event{Type: TypeNote, Agent: agent, Text: text}
}

// IntentEvent builds an intent event: the operator-curated statement of what
// this session is trying to accomplish. Borrowed from miagent's required
// Intent field. Multiple intent events may be written if the goal evolves —
// the latest one wins for board/summary views.
func IntentEvent(agent AgentRef, text string) Event {
	return Event{Type: TypeIntent, Agent: agent, Text: text}
}

// VerificationEvent builds a verification event carrying a verification block
// (status + optional reason + optional evidence). Callers should run
// v.Validate() before Append so malformed verifications never land on disk.
func VerificationEvent(agent AgentRef, v Verification) Event {
	return Event{Type: TypeVerification, Agent: agent, Verification: &v}
}

// HandoffEvent builds a handoff event. summary is the board one-liner; h
// carries the role/awaiting/phase/next state change. Callers should run
// h.Validate() before Append.
func HandoffEvent(agent AgentRef, summary string, h Handoff) Event {
	return Event{Type: TypeHandoff, Agent: agent, Summary: summary, Handoff: &h}
}
