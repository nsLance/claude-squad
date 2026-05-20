package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCodexPrompt(t *testing.T) {
	accept := codexUserLine([]string{"Pull from main branch"})
	if text, ok := parseCodexPrompt([]byte(accept)); !ok || text != "Pull from main branch" {
		t.Errorf("real prompt: got (%q, %v), want (\"Pull from main branch\", true)", text, ok)
	}

	// Real prompt segment after a harness wrapper — codex tends to put them
	// in separate response_items, but defending against the multi-segment
	// case is cheap.
	mixed := codexUserLine([]string{"<environment_context>x</environment_context>", "make it work"})
	if text, ok := parseCodexPrompt([]byte(mixed)); !ok || text != "make it work" {
		t.Errorf("mixed: got (%q, %v), want (\"make it work\", true)", text, ok)
	}

	reject := map[string]string{
		"env_context wrapper":   codexUserLine([]string{"<environment_context>\n  <cwd>/x</cwd>\n</environment_context>"}),
		"permissions wrapper":   codexUserLine([]string{"<permissions instructions>...</permissions instructions>"}),
		"developer role":        codexLineRaw("response_item", "message", "developer", []string{"<permissions instructions>x</permissions instructions>"}),
		"assistant role":        codexLineRaw("response_item", "message", "assistant", []string{"hello"}),
		"reasoning payload":     `{"timestamp":"t","type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
		"non response_item":     `{"timestamp":"t","type":"session_meta","payload":{"cwd":"/x"}}`,
		"empty user input":      codexUserLine([]string{"   "}),
		"not json":              `garbled line`,
	}
	for name, line := range reject {
		if _, ok := parseCodexPrompt([]byte(line)); ok {
			t.Errorf("%s: should have been rejected", name)
		}
	}
}

func TestCodexAdapter_FiltersByCWD(t *testing.T) {
	root, day := codexSessionsDayDir(t)
	cwd := "/work/tree"

	// Two rollouts on the same day: one matching cwd, one not.
	matching := filepath.Join(day, "rollout-001.jsonl")
	other := filepath.Join(day, "rollout-002.jsonl")
	writeLine(t, matching, codexSessionMeta(cwd))
	writeLine(t, other, codexSessionMeta("/other/dir"))

	// Pre-existing prompts in BOTH files must not be replayed (tail-only).
	writeLine(t, matching, codexUserLine([]string{"old in matching"}))
	writeLine(t, other, codexUserLine([]string{"old in other"}))

	a := newCodexAdapterAtDay(root, cwd, day)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &collector{}
	done := make(chan struct{})
	go func() { a.Run(ctx, c.emit); close(done) }()

	time.Sleep(40 * time.Millisecond)
	writeLine(t, matching, codexUserLine([]string{"new prompt in matching"}))
	writeLine(t, other, codexUserLine([]string{"new prompt in other"}))

	waitFor(t, func() bool { return len(c.snapshot()) >= 1 })
	// Give the loop one more poll so anything else that would emit, would.
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done

	got := c.snapshot()
	if len(got) != 1 || got[0] != "new prompt in matching" {
		t.Fatalf("got %v, want [new prompt in matching]", got)
	}
}

func TestCodexAdapter_NewFileAfterRunIsReadWhole(t *testing.T) {
	root, day := codexSessionsDayDir(t)
	cwd := "/work/tree"

	a := newCodexAdapterAtDay(root, cwd, day)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &collector{}
	done := make(chan struct{})
	go func() { a.Run(ctx, c.emit); close(done) }()

	// A fresh rollout that didn't exist at Run start should be read in full.
	time.Sleep(30 * time.Millisecond)
	fresh := filepath.Join(day, "rollout-fresh.jsonl")
	writeLine(t, fresh, codexSessionMeta(cwd))
	writeLine(t, fresh, codexUserLine([]string{"fresh conversation"}))

	waitFor(t, func() bool { return len(c.snapshot()) >= 1 })
	cancel()
	<-done

	if got := c.snapshot(); len(got) != 1 || got[0] != "fresh conversation" {
		t.Fatalf("got %v, want [fresh conversation]", got)
	}
}

// --- helpers ---

// codexSessionsDayDir returns (sessionsRoot, todayDir) — both freshly created
// for the test, pinned to a fixed UTC day so listRollouts() can find them.
func codexSessionsDayDir(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	now := time.Now().UTC()
	day := filepath.Join(root, "sessions",
		now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatalf("mkdir sessions day: %v", err)
	}
	return root, day
}

// newCodexAdapterAtDay builds an adapter whose nowFn returns the timestamp
// matching `day`, so listRollouts() looks in the test's prepared dir.
func newCodexAdapterAtDay(codexHome, cwd, day string) *CodexAdapter {
	a := NewCodexAdapter(codexHome, cwd, "0.1")
	a.pollInterval = 10 * time.Millisecond
	// Derive the fixed time from the day path so the adapter walks our dir.
	dd := filepath.Base(day)
	mm := filepath.Base(filepath.Dir(day))
	yy := filepath.Base(filepath.Dir(filepath.Dir(day)))
	t, err := time.Parse("2006-01-02", yy+"-"+mm+"-"+dd)
	if err != nil {
		panic("test day parse: " + err.Error())
	}
	a.nowFn = func() time.Time { return t }
	return a
}

func codexSessionMeta(cwd string) string {
	b, _ := json.Marshal(map[string]any{
		"timestamp": "2026-05-19T00:00:00Z",
		"type":      "session_meta",
		"payload":   map[string]any{"cwd": cwd, "id": "x", "originator": "codex-tui"},
	})
	return string(b)
}

// codexUserLine builds a response_item with role=user carrying texts.
func codexUserLine(texts []string) string {
	return codexLineRaw("response_item", "message", "user", texts)
}

func codexLineRaw(outerType, innerType, role string, texts []string) string {
	content := make([]map[string]any, 0, len(texts))
	for _, t := range texts {
		content = append(content, map[string]any{"type": "input_text", "text": t})
	}
	b, _ := json.Marshal(map[string]any{
		"timestamp": "2026-05-19T00:00:00Z",
		"type":      outerType,
		"payload": map[string]any{
			"type":    innerType,
			"role":    role,
			"content": content,
		},
	})
	return string(b)
}
