package session

import (
	"strings"
	"testing"

	"claude-squad/session/journal"
)

func TestSummarizeWorkspace_Empty(t *testing.T) {
	ws := t.TempDir()
	got := SummarizeWorkspace(ws)
	if len(got) != 0 {
		t.Fatalf("got %d summaries, want 0", len(got))
	}
}

func TestSummarizeWorkspace_OpenSession(t *testing.T) {
	ws := t.TempDir()
	makeSession(t, ws, "open-one", func(j *journal.Journal) {
		_ = j.Append(journal.HeaderEvent("test"))
		_ = j.Append(journal.IntentEvent(journal.AgentRef{Name: journal.AgentHuman}, "do the thing"))
		_ = j.Append(journal.PromptEvent(journal.AgentRef{Name: journal.AgentClaudeCode}, "go"))
	})

	got := SummarizeWorkspace(ws)
	if len(got) != 1 {
		t.Fatalf("got %d summaries, want 1", len(got))
	}
	s := got[0]
	if s.Slug != "open-one" || s.Status != SessionStatusOpen {
		t.Errorf("slug/status wrong: %+v", s)
	}
	if s.Intent != "do the thing" {
		t.Errorf("intent = %q, want %q", s.Intent, "do the thing")
	}
	if s.Agent != journal.AgentClaudeCode {
		t.Errorf("agent = %q, want %q", s.Agent, journal.AgentClaudeCode)
	}
	if s.Disposition != "" || s.Verification != "" {
		t.Errorf("closed fields should be empty on open session: %+v", s)
	}
}

func TestSummarizeWorkspace_HandoffAndFinish(t *testing.T) {
	ws := t.TempDir()
	makeSession(t, ws, "with-handoff", func(j *journal.Journal) {
		_ = j.Append(journal.HeaderEvent("test"))
		_ = j.Append(journal.HandoffEvent(
			journal.AgentRef{Name: journal.AgentHuman},
			"implementation complete",
			journal.Handoff{Role: "developer", Awaiting: "qa", Phase: "awaiting-qa"},
		))
		_ = j.Append(journal.HandoffEvent(
			journal.AgentRef{Name: journal.AgentHuman},
			"qa picked up",
			journal.Handoff{Role: "qa", Phase: "verification"},
		))
		_ = j.Append(journal.FinishEvent(journal.AgentRef{Name: journal.AgentHuman}, journal.Finish{
			Intent: "x", Work: "y", FilesChanged: []string{"a"},
			Verification: &journal.Verification{Status: journal.VerificationStatusPassed},
			Disposition:  journal.DispositionMerged,
		}))
	})

	got := SummarizeWorkspace(ws)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	s := got[0]
	if s.Status != SessionStatusFinished {
		t.Errorf("status = %q, want %q (finish event present)", s.Status, SessionStatusFinished)
	}
	// The LATEST handoff wins — qa, no awaiting, phase=verification.
	if s.Role != "qa" || s.Awaiting != "" || s.Phase != "verification" {
		t.Errorf("handoff state wrong: %+v", s)
	}
	if s.Disposition != journal.DispositionMerged || s.Verification != journal.VerificationStatusPassed {
		t.Errorf("closure fields wrong: %+v", s)
	}
}

func TestSummarizeWorkspace_SortedBySlug(t *testing.T) {
	ws := t.TempDir()
	for _, slug := range []string{"charlie", "alpha", "bravo"} {
		makeSession(t, ws, slug, func(j *journal.Journal) {
			_ = j.Append(journal.HeaderEvent("test"))
		})
	}
	got := SummarizeWorkspace(ws)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, s := range got {
		if s.Slug != want[i] {
			t.Errorf("got[%d].Slug = %q, want %q", i, s.Slug, want[i])
		}
	}
}

func TestFormatSummaryTSV_HasAllColumns(t *testing.T) {
	header := SummaryTSVHeader()
	cols := strings.Split(header, "\t")
	if len(cols) != 10 {
		t.Fatalf("header has %d columns, want 10", len(cols))
	}

	row := FormatSummaryTSV(SessionSummary{
		Slug: "x", Status: SessionStatusFinished, Disposition: journal.DispositionMerged,
		Verification: journal.VerificationStatusPassed,
	})
	parts := strings.Split(row, "\t")
	if len(parts) != 10 {
		t.Fatalf("row has %d columns, want 10: %q", len(parts), row)
	}
	if parts[0] != "x" || parts[1] != SessionStatusFinished {
		t.Errorf("row layout wrong: %v", parts)
	}
}
