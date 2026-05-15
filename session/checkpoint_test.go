package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"claude-squad/session/journal"
)

// newJournalWithEvents writes a fresh journal seeded with a header and the
// given note texts, then returns its path.
func newJournalWithEvents(t *testing.T, notes ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, _, err := journal.Open(path, journal.SessionRef{ID: "sid", Title: "t"}, "ws")
	if err != nil {
		t.Fatalf("Open journal: %v", err)
	}
	if err := j.Append(journal.HeaderEvent("test")); err != nil {
		t.Fatalf("append header: %v", err)
	}
	for _, n := range notes {
		if err := j.Append(journal.NoteEvent(journal.AgentRef{Name: journal.AgentHuman}, n)); err != nil {
			t.Fatalf("append note: %v", err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}
	return path
}

func readJournalEvents(t *testing.T, path string) []journal.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer f.Close()
	var out []journal.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e journal.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func opts(path, summary string) CheckpointOptions {
	return CheckpointOptions{
		JournalPath: path,
		Session:     journal.SessionRef{ID: "sid", Title: "t"},
		Workspace:   "ws",
		Summary:     summary,
		// WorktreePath left empty: git is skipped, so the test needs no repo.
	}
}

func TestCreateCheckpoint_FirstCheckpoint(t *testing.T) {
	path := newJournalWithEvents(t, "did a thing")

	sig, err := CreateCheckpoint(opts(path, "first checkpoint"))
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if sig.Prev != "" {
		t.Errorf("first checkpoint Prev = %q, want empty", sig.Prev)
	}
	if sig.From != 0 {
		t.Errorf("first checkpoint From = %d, want 0", sig.From)
	}
	if sig.Hash == "" || sig.To <= sig.From {
		t.Errorf("bad signature: %+v", sig)
	}

	events := readJournalEvents(t, path)
	last := events[len(events)-1]
	if last.Type != journal.TypeCheckpoint {
		t.Fatalf("last event type = %q, want checkpoint", last.Type)
	}
	if last.Summary != "first checkpoint" || last.Signature == nil {
		t.Fatalf("checkpoint event malformed: %+v", last)
	}
	if last.Signature.Hash != sig.Hash {
		t.Errorf("event signature hash %q != returned %q", last.Signature.Hash, sig.Hash)
	}
}

func TestCreateCheckpoint_ChainsToPrevious(t *testing.T) {
	path := newJournalWithEvents(t, "work one")

	first, err := CreateCheckpoint(opts(path, "checkpoint one"))
	if err != nil {
		t.Fatalf("first CreateCheckpoint: %v", err)
	}

	// More activity, then a second checkpoint.
	j, err := journal.Reopen(path, journal.SessionRef{ID: "sid", Title: "t"}, "ws")
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	_ = j.Append(journal.NoteEvent(journal.AgentRef{Name: journal.AgentHuman}, "work two"))
	_ = j.Close()

	second, err := CreateCheckpoint(opts(path, "checkpoint two"))
	if err != nil {
		t.Fatalf("second CreateCheckpoint: %v", err)
	}

	if second.Prev != first.Hash {
		t.Errorf("second.Prev = %q, want first.Hash %q", second.Prev, first.Hash)
	}
	if second.From != first.To {
		t.Errorf("second.From = %d, want first.To %d (no gap, no overlap)", second.From, first.To)
	}
	if second.Hash == first.Hash {
		t.Error("distinct checkpoints produced identical hashes")
	}
}

func TestCreateCheckpoint_SignatureVerifies(t *testing.T) {
	path := newJournalWithEvents(t, "some work")
	sig, err := CreateCheckpoint(opts(path, "verify me"))
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// Recompute the signature from the on-disk journal range and confirm it
	// matches what was recorded — i.e. the chain is independently verifiable.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	want := journal.ComputeSignature(sig.Prev, data[sig.From:sig.To], "verify me", "")
	if want != sig.Hash {
		t.Errorf("recomputed signature %q != recorded %q", want, sig.Hash)
	}
}

func TestCreateCheckpoint_RejectsEmptySummary(t *testing.T) {
	path := newJournalWithEvents(t)
	if _, err := CreateCheckpoint(opts(path, "   ")); err == nil {
		t.Error("expected an error for an empty summary")
	}
}

func TestCreateCheckpoint_RejectsMissingJournal(t *testing.T) {
	if _, err := CreateCheckpoint(opts("", "x")); err == nil {
		t.Error("expected an error when no journal path is given")
	}
}
