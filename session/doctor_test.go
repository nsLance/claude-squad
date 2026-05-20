package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-squad/session/journal"
)

func makeSession(t *testing.T, wsDir, slug string, build func(j *journal.Journal)) string {
	t.Helper()
	sessDir := filepath.Join(wsDir, "sessions", slug)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	path := filepath.Join(sessDir, "journal.jsonl")
	j, _, err := journal.Open(path, journal.SessionRef{ID: "sid", Title: slug}, "ws")
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	build(j)
	if err := j.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}
	return path
}

func findingsBy(t *testing.T, fs []Finding, category string) []Finding {
	t.Helper()
	var out []Finding
	for _, f := range fs {
		if f.Category == category {
			out = append(out, f)
		}
	}
	return out
}

func TestDoctor_HappyPath(t *testing.T) {
	ws := t.TempDir()
	path := makeSession(t, ws, "ok-session", func(j *journal.Journal) {
		_ = j.Append(journal.HeaderEvent("test"))
		_ = j.Append(journal.NoteEvent(journal.AgentRef{Name: journal.AgentHuman}, "did stuff"))
		_ = j.Append(journal.FinishEvent(journal.AgentRef{Name: journal.AgentHuman}, journal.Finish{
			Intent: "x", Work: "y", FilesChanged: []string{"a"},
			Verification: &journal.Verification{Status: journal.VerificationStatusPassed},
			Disposition:  journal.DispositionMerged,
		}))
	})
	_ = path

	got := RunDoctor(ws)
	if len(got) != 0 {
		t.Fatalf("got %d findings, want 0: %+v", len(got), got)
	}
	if HasErrors(got) {
		t.Error("HasErrors should be false on a clean workspace")
	}
}

func TestDoctor_MissingHeader(t *testing.T) {
	ws := t.TempDir()
	makeSession(t, ws, "no-header", func(j *journal.Journal) {
		// Skip the header; just append a note. doctor must catch this.
		_ = j.Append(journal.NoteEvent(journal.AgentRef{Name: journal.AgentHuman}, "x"))
	})

	got := RunDoctor(ws)
	rh := findingsBy(t, got, CategoryRecordHealth)
	if len(rh) == 0 {
		t.Fatalf("expected record-health findings, got %+v", got)
	}
	if !HasErrors(got) {
		t.Error("missing header should produce an error-severity finding")
	}
}

func TestDoctor_MalformedJSON(t *testing.T) {
	ws := t.TempDir()
	sessDir := filepath.Join(ws, "sessions", "bad-json")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sessDir, "journal.jsonl")
	if err := os.WriteFile(path, []byte(`{"v":1,"type":"header"}`+"\n"+`{not json`+"\n"), 0o644); err != nil {
		t.Fatalf("write malformed journal: %v", err)
	}

	got := RunDoctor(ws)
	if !HasErrors(got) {
		t.Errorf("malformed JSON should be an error: %+v", got)
	}
	found := false
	for _, f := range got {
		if strings.Contains(f.Message, "malformed JSON") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a malformed-JSON message, got %+v", got)
	}
}

func TestDoctor_StalenessVsClosure(t *testing.T) {
	ws := t.TempDir()
	stale := makeSession(t, ws, "stale-one", func(j *journal.Journal) {
		_ = j.Append(journal.HeaderEvent("test"))
	})
	fresh := makeSession(t, ws, "fresh-one", func(j *journal.Journal) {
		_ = j.Append(journal.HeaderEvent("test"))
	})

	// Backdate the stale session's mtime to triple the threshold.
	old := time.Now().Add(-3 * stalenessThreshold)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got := RunDoctor(ws)

	// Stale session should produce a staleness finding (warning).
	var staleness, closure []Finding
	for _, f := range got {
		switch f.Category {
		case CategoryStaleness:
			staleness = append(staleness, f)
		case CategoryClosure:
			closure = append(closure, f)
		}
	}
	if len(staleness) != 1 || staleness[0].Path != "stale-one" || staleness[0].Severity != SeverityWarning {
		t.Errorf("staleness findings = %+v, want one warning for stale-one", staleness)
	}
	if len(closure) != 1 || closure[0].Path != "fresh-one" || closure[0].Severity != SeverityInfo {
		t.Errorf("closure findings = %+v, want one info for fresh-one", closure)
	}

	_ = fresh
}

func TestDoctor_UnknownEventType(t *testing.T) {
	ws := t.TempDir()
	sessDir := filepath.Join(ws, "sessions", "future-event")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sessDir, "journal.jsonl")
	body := `{"v":1,"type":"header"}` + "\n" + `{"v":1,"type":"never-heard-of-it"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := RunDoctor(ws)
	found := false
	for _, f := range got {
		if f.Severity == SeverityWarning && strings.Contains(f.Message, "unknown event type") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning for an unknown event type, got %+v", got)
	}
}

func TestFormatFinding(t *testing.T) {
	got := FormatFinding(Finding{
		Severity: SeverityWarning, Category: CategoryStaleness,
		Path: "abc-deadbeef", Message: "stale: 30d since last activity",
	})
	want := "warning\tstaleness\tabc-deadbeef\tstale: 30d since last activity"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
