package session

import (
	"strings"
	"testing"

	"claude-squad/session/journal"
)

func validFinish() journal.Finish {
	return journal.Finish{
		Intent:       "ship the codex adapter",
		Work:         "wrote adapter, wired into instance_journal, tests pass",
		FilesChanged: []string{"session/journal/adapter_codex.go"},
		Verification: &journal.Verification{
			Status:   journal.VerificationStatusPassed,
			Evidence: "go test ./... -count=1: all green",
		},
		Disposition: journal.DispositionMerged,
	}
}

func finishOpts(path string, f journal.Finish) FinishOptions {
	return FinishOptions{
		JournalPath: path,
		Session:     journal.SessionRef{ID: "sid", Title: "t"},
		Workspace:   "ws",
		Finish:      f,
	}
}

func TestCreateFinish_HappyPath(t *testing.T) {
	path := newJournalWithEvents(t, "some work")
	if err := CreateFinish(finishOpts(path, validFinish())); err != nil {
		t.Fatalf("CreateFinish: %v", err)
	}

	events := readJournalEvents(t, path)
	last := events[len(events)-1]
	if last.Type != journal.TypeFinish {
		t.Fatalf("last event type = %q, want finish", last.Type)
	}
	if last.Finish == nil || last.Finish.Disposition != journal.DispositionMerged {
		t.Fatalf("finish payload missing/wrong: %+v", last.Finish)
	}
	if last.Finish.Verification == nil || last.Finish.Verification.Status != journal.VerificationStatusPassed {
		t.Fatalf("verification block missing: %+v", last.Finish)
	}
}

func TestCreateFinish_RejectsMalformedBeforeWriting(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(f *journal.Finish)
		token string
	}{
		{"no intent", func(f *journal.Finish) { f.Intent = "" }, "intent"},
		{"no work", func(f *journal.Finish) { f.Work = "" }, "work"},
		{"no files and no_files=false", func(f *journal.Finish) { f.FilesChanged = nil }, "files_changed"},
		{"missing verification", func(f *journal.Finish) { f.Verification = nil }, "verification"},
		{"bad disposition", func(f *journal.Finish) { f.Disposition = "shipped" }, "disposition"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := newJournalWithEvents(t, "x")
			before := len(readJournalEvents(t, path))

			f := validFinish()
			c.mut(&f)
			err := CreateFinish(finishOpts(path, f))
			if err == nil {
				t.Fatalf("expected error containing %q", c.token)
			}
			if !strings.Contains(err.Error(), c.token) {
				t.Errorf("error %q does not contain %q", err.Error(), c.token)
			}
			after := len(readJournalEvents(t, path))
			if after != before {
				t.Errorf("validation must run before write: journal grew from %d to %d events", before, after)
			}
		})
	}
}

func TestCreateFinish_NoFilesAcceptable(t *testing.T) {
	path := newJournalWithEvents(t, "doc-only session")
	f := validFinish()
	f.FilesChanged = nil
	f.NoFiles = true
	if err := CreateFinish(finishOpts(path, f)); err != nil {
		t.Fatalf("CreateFinish with no_files=true: %v", err)
	}
}

func TestCreateFinish_RejectsMissingJournal(t *testing.T) {
	if err := CreateFinish(finishOpts("", validFinish())); err == nil {
		t.Error("expected an error when no journal path is given")
	}
}
