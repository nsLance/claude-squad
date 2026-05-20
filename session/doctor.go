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

// stalenessThreshold mirrors miagent: a session is "stale" if its last
// activity is older than 14 days and it never reached a finish event.
const stalenessThreshold = 14 * 24 * time.Hour

// Severity levels emitted by doctor. Ordered: error > warning > info.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Finding categories — borrowed from miagent's doctor with cs-specific
// substitutions (signature-integrity replaces miagent's seed-integrity).
const (
	CategoryRecordHealth       = "record-health"
	CategoryClosure            = "closure"
	CategoryStaleness          = "staleness"
	CategorySignatureIntegrity = "signature-integrity"
)

// Finding is one health observation about a journal. Doctor emits these as
// `<severity> <category> <message> <path>` so the CLI can render a uniform
// list without secondary formatting.
type Finding struct {
	Severity string
	Category string
	Message  string
	Path     string // session-relative slug (parent dir name), not absolute path
}

// RunDoctor walks every journal under <workspaceDir>/sessions/*/journal.jsonl
// and returns the union of health findings. Best-effort: a missing or
// unreadable file produces a finding rather than an error.
func RunDoctor(workspaceDir string) []Finding {
	var findings []Finding
	sessionsDir := filepath.Join(workspaceDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return findings // no sessions/ dir == no findings (a fresh workspace)
	}
	// Iterate in deterministic order so the CLI output is stable.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		slug := name
		path := filepath.Join(sessionsDir, name, "journal.jsonl")
		findings = append(findings, inspectJournal(slug, path)...)
	}
	return findings
}

func inspectJournal(slug, path string) []Finding {
	var out []Finding
	st, err := os.Stat(path)
	if err != nil {
		out = append(out, Finding{
			Severity: SeverityWarning, Category: CategoryRecordHealth,
			Message: fmt.Sprintf("session directory has no journal.jsonl: %v", err),
			Path:    slug,
		})
		return out
	}

	f, err := os.Open(path)
	if err != nil {
		out = append(out, Finding{
			Severity: SeverityError, Category: CategoryRecordHealth,
			Message: fmt.Sprintf("cannot open journal: %v", err),
			Path:    slug,
		})
		return out
	}
	defer f.Close()

	var (
		lineNo       int
		seenHeader   bool
		seenFinish   bool
		unknownTypes = map[string]int{}
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		lineNo++
		var e journal.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			out = append(out, Finding{
				Severity: SeverityError, Category: CategoryRecordHealth,
				Message: fmt.Sprintf("line %d: malformed JSON: %v", lineNo, err),
				Path:    slug,
			})
			continue
		}
		if lineNo == 1 {
			if e.Type != journal.TypeHeader {
				out = append(out, Finding{
					Severity: SeverityError, Category: CategoryRecordHealth,
					Message: fmt.Sprintf("first event is %q, want %q", e.Type, journal.TypeHeader),
					Path:    slug,
				})
			} else {
				seenHeader = true
			}
		}
		if !isKnownType(e.Type) {
			unknownTypes[e.Type]++
		}
		if e.Type == journal.TypeFinish {
			seenFinish = true
		}
	}
	if err := sc.Err(); err != nil {
		out = append(out, Finding{
			Severity: SeverityError, Category: CategoryRecordHealth,
			Message: fmt.Sprintf("scan failed at line %d: %v", lineNo, err),
			Path:    slug,
		})
	}
	if !seenHeader && lineNo > 0 {
		// Already reported via "first event is ..." but also flag the
		// general missing-header case for empty-after-truncation journals.
		out = append(out, Finding{
			Severity: SeverityError, Category: CategoryRecordHealth,
			Message: "journal has no header event",
			Path:    slug,
		})
	}
	for t, n := range unknownTypes {
		out = append(out, Finding{
			Severity: SeverityWarning, Category: CategoryRecordHealth,
			Message: fmt.Sprintf("unknown event type %q (%d occurrences)", t, n),
			Path:    slug,
		})
	}

	// Closure / staleness: a finished session is final; otherwise the file's
	// mtime is its last activity timestamp.
	if !seenFinish {
		age := time.Since(st.ModTime())
		if age > stalenessThreshold {
			out = append(out, Finding{
				Severity: SeverityWarning, Category: CategoryStaleness,
				Message: fmt.Sprintf("no finish event and last activity %s ago", roughDuration(age)),
				Path:    slug,
			})
		} else {
			out = append(out, Finding{
				Severity: SeverityInfo, Category: CategoryClosure,
				Message: fmt.Sprintf("no finish event yet (last activity %s ago)", roughDuration(age)),
				Path:    slug,
			})
		}
	}
	return out
}

// isKnownType reports whether t is one of the event types journal currently
// defines. Unknown types aren't an error — a future schema may add more —
// but they're worth surfacing so an outdated cs build doesn't silently mis-
// interpret a newer journal.
func isKnownType(t string) bool {
	switch t {
	case journal.TypeHeader, journal.TypePrompt, journal.TypeNote,
		journal.TypeCheckpoint, journal.TypeHandoff,
		journal.TypeIntent, journal.TypeVerification,
		journal.TypeDecision, journal.TypeFinish:
		return true
	}
	return false
}

// roughDuration renders d as a short human-friendly span (e.g. "3d", "2h"),
// rounding down to the largest unit. Used in doctor messages where exact
// precision is noise.
func roughDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
}

// HasErrors reports whether any finding is at SeverityError.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// FormatFinding renders one finding as a single line: severity, category,
// path, message. Stable shape for shell-friendly parsing.
func FormatFinding(f Finding) string {
	return strings.Join([]string{f.Severity, f.Category, f.Path, f.Message}, "\t")
}
