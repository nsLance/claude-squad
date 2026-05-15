package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{26}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if !re.MatchString(id) {
			t.Fatalf("NewID() = %q, want 26 lowercase hex chars", id)
		}
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}

func TestNewSessionID_IsUUIDv4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 100; i++ {
		id := NewSessionID()
		if !re.MatchString(id) {
			t.Fatalf("NewSessionID() = %q, not a v4 UUID", id)
		}
	}
}

func TestComputeSignature_DeterministicAndSensitive(t *testing.T) {
	base := ComputeSignature("prev0", []byte("range bytes"), "did things", "abc123")
	if base != ComputeSignature("prev0", []byte("range bytes"), "did things", "abc123") {
		t.Fatal("ComputeSignature is not deterministic")
	}
	// Each input must affect the hash.
	cases := map[string]string{
		"prev":    ComputeSignature("PREV", []byte("range bytes"), "did things", "abc123"),
		"range":   ComputeSignature("prev0", []byte("RANGE bytes"), "did things", "abc123"),
		"summary": ComputeSignature("prev0", []byte("range bytes"), "DID things", "abc123"),
		"gitSHA":  ComputeSignature("prev0", []byte("range bytes"), "did things", "DEF456"),
	}
	for field, got := range cases {
		if got == base {
			t.Errorf("changing %s did not change the signature", field)
		}
	}
}

func TestComputeSignature_NoFieldBleed(t *testing.T) {
	// The 0x00 separators must prevent field-boundary ambiguity: moving a
	// character across a boundary must change the hash.
	a := ComputeSignature("ab", []byte("c"), "d", "e")
	b := ComputeSignature("a", []byte("bc"), "d", "e")
	if a == b {
		t.Fatal("field boundary not enforced — separators ineffective")
	}
}

func TestJournal_AppendRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "journal.jsonl")
	sess := SessionRef{ID: "sid-1", Title: "my session"}

	j, isNew, err := Open(path, sess, "ws-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isNew {
		t.Fatal("Open: isNew = false for a fresh file")
	}

	if err := j.Append(HeaderEvent("1.0.17")); err != nil {
		t.Fatalf("Append header: %v", err)
	}
	if err := j.Append(PromptEvent(AgentRef{Name: AgentClaudeCode, Version: "2.0"}, "do the thing")); err != nil {
		t.Fatalf("Append prompt: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readEvents(t, path)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	h := events[0]
	if h.Type != TypeHeader || h.CSVersion != "1.0.17" {
		t.Errorf("header event wrong: %+v", h)
	}
	if h.V != SchemaVersion {
		t.Errorf("header V = %d, want %d", h.V, SchemaVersion)
	}
	if h.ID == "" || h.TS.IsZero() {
		t.Errorf("header envelope not stamped: %+v", h)
	}
	if h.Session != sess || h.Workspace != "ws-1" {
		t.Errorf("header session/workspace = %+v / %q, want %+v / ws-1", h.Session, h.Workspace, sess)
	}

	p := events[1]
	if p.Type != TypePrompt || p.Text != "do the thing" || p.Agent.Name != AgentClaudeCode {
		t.Errorf("prompt event wrong: %+v", p)
	}
}

func TestJournal_ReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	sess := SessionRef{ID: "sid-2", Title: "s"}

	j, _, err := Open(path, sess, "ws")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Append(HeaderEvent("v")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = j.Close()

	// A separate process (modeled here as Reopen) appends without a header.
	j2, err := Reopen(path, sess, "ws")
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if err := j2.Append(NoteEvent(AgentRef{Name: AgentHuman}, "a note")); err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	_ = j2.Close()

	events := readEvents(t, path)
	if len(events) != 2 || events[0].Type != TypeHeader || events[1].Type != TypeNote {
		t.Fatalf("reopen did not append cleanly: %+v", events)
	}
}

func TestReopen_MissingFileErrors(t *testing.T) {
	_, err := Reopen(filepath.Join(t.TempDir(), "nope.jsonl"), SessionRef{}, "")
	if err == nil {
		t.Fatal("Reopen of a missing file should error")
	}
}

func TestJournal_ReadRangeMatchesOnDiskBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, _, err := Open(path, SessionRef{ID: "s"}, "ws")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Append(HeaderEvent("v")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mid, err := j.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if err := j.Append(NoteEvent(AgentRef{Name: AgentCS}, "tail")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	end, _ := j.Size()

	tail, err := j.ReadRange(mid, end)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	full, _ := os.ReadFile(path)
	if string(tail) != string(full[mid:end]) {
		t.Errorf("ReadRange tail mismatch:\n got %q\nwant %q", tail, full[mid:end])
	}
	if !strings.Contains(string(tail), "tail") {
		t.Errorf("ReadRange did not capture the second event: %q", tail)
	}
	_ = j.Close()
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open journal for read: %v", err)
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal event %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan journal: %v", err)
	}
	return out
}
