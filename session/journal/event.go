// Package journal implements the per-session structured event log: an
// append-only JSONL file recording prompts, notes, checkpoints, and handoffs
// so multiple agents can audit and resume work across sessions.
//
// Journaling is best-effort. Callers log Open/Append errors as warnings and
// never fail a session because the journal could not be written.
package journal

import (
	"crypto/rand"
	"fmt"
	"time"
)

// SchemaVersion is stamped on every event as `v`. Bump on incompatible
// envelope or payload changes.
const SchemaVersion = 1

// Event types. Exactly one `header` per file; the rest may repeat.
const (
	TypeHeader     = "header"     // once per file: cs_version + envelope
	TypePrompt     = "prompt"     // a real user prompt, captured passively by an adapter
	TypeNote       = "note"       // a manual note
	TypeCheckpoint = "checkpoint" // user-triggered signed checkpoint
	TypeHandoff    = "handoff"    // user-triggered cross-agent handoff
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
