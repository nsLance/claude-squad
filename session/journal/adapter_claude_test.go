package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		"/Users/foo/bar":             "-Users-foo-bar",
		"/Users/nakkul/Workspace/cs": "-Users-nakkul-Workspace-cs",
		"/tmp/a.b_c-d":               "-tmp-a-b-c-d",
		"plain":                      "plain",
	}
	for in, want := range cases {
		if got := EncodeProjectPath(in); got != want {
			t.Errorf("EncodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseClaudePrompt(t *testing.T) {
	accept := `{"type":"user","userType":"external","isSidechain":false,"message":{"content":"  fix the bug  "}}`
	if text, ok := parseClaudePrompt([]byte(accept)); !ok || text != "fix the bug" {
		t.Errorf("real prompt: got (%q, %v), want (\"fix the bug\", true)", text, ok)
	}

	reject := map[string]string{
		"sidechain":     `{"type":"user","userType":"external","isSidechain":true,"message":{"content":"x"}}`,
		"not external":  `{"type":"user","userType":"internal","isSidechain":false,"message":{"content":"x"}}`,
		"not user":      `{"type":"assistant","userType":"external","isSidechain":false,"message":{"content":"x"}}`,
		"array content": `{"type":"user","userType":"external","isSidechain":false,"message":{"content":[{"type":"tool_result"}]}}`,
		"empty string":  `{"type":"user","userType":"external","isSidechain":false,"message":{"content":"   "}}`,
		"not json":      `not json at all`,
	}
	for name, line := range reject {
		if _, ok := parseClaudePrompt([]byte(line)); ok {
			t.Errorf("%s: should have been rejected", name)
		}
	}
}

// collector is a concurrency-safe emit sink for tests.
type collector struct {
	mu   sync.Mutex
	text []string
}

func (c *collector) emit(_ AgentRef, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = append(c.text, text)
}

func (c *collector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.text...)
}

func writeLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	_ = f.Close()
}

// sprintfPrompt builds a claude-code transcript line that parseClaudePrompt
// accepts as a real user prompt carrying the given text.
func sprintfPrompt(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type":        "user",
		"userType":    "external",
		"isSidechain": false,
		"message":     map[string]any{"content": text},
	})
	return string(b)
}

func TestClaudeAdapter_TailOnly_NoHistoryReplay(t *testing.T) {
	dir := t.TempDir()
	cwd := "/work/tree"
	projDir := filepath.Join(dir, "projects", EncodeProjectPath(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcript := filepath.Join(projDir, "conv.jsonl")

	// Pre-existing history must NOT be replayed.
	writeLine(t, transcript, sprintfPrompt("old prompt before run"))

	a := NewClaudeAdapter(dir, cwd, "2.0")
	a.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &collector{}
	done := make(chan struct{})
	go func() { a.Run(ctx, c.emit); close(done) }()

	// Append new prompts after Run has started.
	time.Sleep(40 * time.Millisecond)
	writeLine(t, transcript, sprintfPrompt("new prompt one"))
	writeLine(t, transcript, sprintfPrompt("new prompt two"))

	waitFor(t, func() bool { return len(c.snapshot()) >= 2 })
	cancel()
	<-done

	got := c.snapshot()
	for _, g := range got {
		if g == "old prompt before run" {
			t.Fatal("tail-only violated: replayed pre-existing history")
		}
	}
	if len(got) != 2 || got[0] != "new prompt one" || got[1] != "new prompt two" {
		t.Fatalf("got %v, want [new prompt one, new prompt two]", got)
	}
}

func TestClaudeAdapter_NewFileAfterRunIsReadWhole(t *testing.T) {
	dir := t.TempDir()
	cwd := "/work/tree"
	projDir := filepath.Join(dir, "projects", EncodeProjectPath(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	a := NewClaudeAdapter(dir, cwd, "")
	a.pollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &collector{}
	done := make(chan struct{})
	go func() { a.Run(ctx, c.emit); close(done) }()

	// A transcript that appears entirely after Run started is a brand-new
	// conversation — its full contents should be picked up.
	time.Sleep(30 * time.Millisecond)
	writeLine(t, filepath.Join(projDir, "fresh.jsonl"), sprintfPrompt("fresh conversation"))

	waitFor(t, func() bool { return len(c.snapshot()) >= 1 })
	cancel()
	<-done

	if got := c.snapshot(); len(got) != 1 || got[0] != "fresh conversation" {
		t.Fatalf("got %v, want [fresh conversation]", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
