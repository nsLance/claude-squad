package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claude-squad/session/journal"
)

// Session lifecycle statuses derived from journal contents. Distinct from
// instance.Status (Running/Paused/...): those are pane-process states, these
// are workspace-level audit states borrowed from miagent's board.
const (
	SessionStatusOpen           = "open"
	SessionStatusFinished       = "finished"
	SessionStatusAbandoned      = "abandoned"
	SessionStatusHandedOff      = "handed-off"
	SessionStatusAwaitingReview = "awaiting-review"
)

// SessionSummary is the rolled-up view of one session's journal — what the
// board view renders, what `cs sessions` prints, what miagent's `miagent
// board` would call a row.
type SessionSummary struct {
	Slug         string
	Title        string
	Agent        string    // last agent.name that wrote a non-cs event
	LastActivity time.Time // journal file mtime
	Intent       string    // latest intent event text (one-line summary)

	// Latest handoff state — empty when no handoff has occurred. Awaiting=""
	// means within-task.
	Role     string
	Awaiting string
	Phase    string

	// Closure — populated only when a finish event exists.
	Status       string
	Disposition  string
	Verification string
}

// SummarizeWorkspace walks every <workspaceDir>/sessions/*/journal.jsonl and
// returns one SessionSummary per session, sorted by slug.
func SummarizeWorkspace(workspaceDir string) []SessionSummary {
	sessionsDir := filepath.Join(workspaceDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]SessionSummary, 0, len(names))
	for _, name := range names {
		s := summarizeJournal(name, filepath.Join(sessionsDir, name, "journal.jsonl"))
		out = append(out, s)
	}
	return out
}

func summarizeJournal(slug, path string) SessionSummary {
	s := SessionSummary{Slug: slug, Status: SessionStatusOpen}
	st, err := os.Stat(path)
	if err == nil {
		s.LastActivity = st.ModTime()
	}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e journal.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Session.Title != "" {
			s.Title = e.Session.Title
		}
		if e.Agent.Name != "" && e.Agent.Name != journal.AgentCS {
			s.Agent = e.Agent.Name
		}
		switch e.Type {
		case journal.TypeIntent:
			if e.Text != "" {
				s.Intent = e.Text
			}
		case journal.TypeHandoff:
			if e.Handoff != nil {
				s.Role = e.Handoff.Role
				s.Awaiting = e.Handoff.Awaiting
				s.Phase = e.Handoff.Phase
			}
		case journal.TypeFinish:
			if e.Finish != nil {
				s.Disposition = e.Finish.Disposition
				if e.Finish.Verification != nil {
					s.Verification = e.Finish.Verification.Status
				}
				s.Status = statusFromDisposition(s.Disposition)
			}
		}
	}
	return s
}

func statusFromDisposition(d string) string {
	switch d {
	case journal.DispositionMerged:
		return SessionStatusFinished
	case journal.DispositionAbandoned:
		return SessionStatusAbandoned
	case journal.DispositionHandedOff:
		return SessionStatusHandedOff
	case journal.DispositionOther:
		return SessionStatusFinished
	}
	return SessionStatusOpen
}

// FormatSummaryTSV renders a SessionSummary as a tab-separated row matching
// miagent's worklog columns where they overlap, with cs-specific extras. The
// header row produced by SummaryTSVHeader names these columns.
func FormatSummaryTSV(s SessionSummary) string {
	return strings.Join([]string{
		s.Slug,
		s.Status,
		formatTime(s.LastActivity),
		emptyDash(s.Agent),
		emptyDash(s.Role),
		emptyDash(s.Awaiting),
		emptyDash(s.Phase),
		emptyDash(s.Disposition),
		emptyDash(s.Verification),
		oneLine(s.Intent),
	}, "\t")
}

// SummaryTSVHeader is the column header for FormatSummaryTSV output.
func SummaryTSVHeader() string {
	return strings.Join([]string{
		"slug", "status", "last_activity", "agent",
		"role", "awaiting", "phase",
		"disposition", "verification", "intent",
	}, "\t")
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return emptyDash(s)
}

// PrintSummaryTable writes a human-friendly table of summaries to w.
// Columns are padded to align by max width per column.
func PrintSummaryTable(w *os.File, summaries []SessionSummary) {
	if len(summaries) == 0 {
		fmt.Fprintln(w, "no sessions yet in this workspace")
		return
	}
	rows := make([][]string, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, []string{
			s.Slug,
			s.Status,
			roughDurationFromNow(s.LastActivity),
			emptyDash(s.Agent),
			roleAwaiting(s),
			emptyDash(s.Verification),
			oneLine(s.Intent),
		})
	}
	headers := []string{"SLUG", "STATUS", "LAST", "AGENT", "ROLE", "VERIF", "INTENT"}
	cols := len(headers)
	widths := make([]int, cols)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, v := range r {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}
	printRow := func(r []string) {
		for i, v := range r {
			pad := widths[i] - len(v)
			fmt.Fprint(w, v)
			if i < cols-1 {
				fmt.Fprint(w, strings.Repeat(" ", pad+2))
			}
		}
		fmt.Fprintln(w)
	}
	printRow(headers)
	for _, r := range rows {
		printRow(r)
	}
}

func roleAwaiting(s SessionSummary) string {
	if s.Role == "" && s.Awaiting == "" {
		return "-"
	}
	if s.Awaiting == "" {
		return s.Role
	}
	return s.Role + "→" + s.Awaiting
}

func roughDurationFromNow(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return roughDuration(time.Since(t)) + " ago"
}
