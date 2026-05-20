package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// codexPollInterval is how often the adapter re-scans the sessions dir.
const codexPollInterval = time.Second

// CodexAdapter watches codex's own JSONL rollout files for a worktree and
// reports real user prompts. Codex stores rollouts globally at
// <CODEX_HOME>/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl, so the adapter
// scopes to today's directory and filters by session_meta.cwd.
type CodexAdapter struct {
	sessionsDir  string
	cwd          string
	version      string
	pollInterval time.Duration
	// nowFn is overridable for tests.
	nowFn func() time.Time
}

// NewCodexAdapter builds an adapter for a codex session running in worktree
// cwd. codexHome is the resolved CODEX_HOME (default ~/.codex); version is
// the codex CLI version if known, else "".
func NewCodexAdapter(codexHome, cwd, version string) *CodexAdapter {
	return &CodexAdapter{
		sessionsDir:  filepath.Join(codexHome, "sessions"),
		cwd:          cwd,
		version:      version,
		pollInterval: codexPollInterval,
		nowFn:        func() time.Time { return time.Now().UTC() },
	}
}

// Run implements Adapter. It polls today's rollout directory each tick,
// draining new lines from rollouts whose session_meta.cwd matches our
// worktree, and emits any real user prompts found.
func (a *CodexAdapter) Run(ctx context.Context, emit func(AgentRef, string)) {
	agent := AgentRef{Name: AgentCodex, Version: a.version}

	offsets := map[string]int64{} // bytes consumed per file
	// classified[path]: 1 = cwd matches, -1 = no match, absent = unknown.
	classified := map[string]int8{}

	// Tail-only: every rollout already present at start is seeded to its
	// current EOF, so we never replay history. Files that appear later are
	// new sessions and read from offset 0.
	for _, f := range a.listRollouts() {
		if st, err := os.Stat(f); err == nil {
			offsets[f] = st.Size()
		}
	}

	t := time.NewTicker(a.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, f := range a.listRollouts() {
				switch classified[f] {
				case -1:
					continue
				case 0:
					c := a.classify(f)
					classified[f] = c
					if c != 1 {
						continue
					}
				}
				offsets[f] = a.drain(f, offsets[f], agent, emit)
			}
		}
	}
}

// listRollouts returns rollout-*.jsonl files in today's YYYY/MM/DD subdir.
// Codex creates one rollout per session at session start, so an in-progress
// session's file lives in the day it began; for our tail-only purposes,
// scanning today is enough — long-running sessions that cross midnight keep
// appending to yesterday's file but we already seeded their offsets.
func (a *CodexAdapter) listRollouts() []string {
	now := a.nowFn()
	dir := filepath.Join(a.sessionsDir,
		now.Format("2006"), now.Format("01"), now.Format("02"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, ".jsonl") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// classify reads the first line of a rollout and returns 1 if its
// session_meta.cwd matches our worktree, -1 otherwise. Returns 0 only if the
// header has not yet been flushed.
func (a *CodexAdapter) classify(path string) int8 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !sc.Scan() {
		return 0
	}
	var meta codexLine
	if err := json.Unmarshal(sc.Bytes(), &meta); err != nil || meta.Type != "session_meta" {
		return -1
	}
	if meta.Payload.CWD == a.cwd {
		return 1
	}
	return -1
}

// drain reads complete newline-terminated lines from path starting at offset,
// emits the ones that are real user prompts, and returns the new offset.
func (a *CodexAdapter) drain(path string, offset int64, agent AgentRef, emit func(AgentRef, string)) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0
	}
	if st.Size() == offset {
		return offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return offset
	}
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return offset
	}
	complete := data[:lastNL+1]

	sc := bufio.NewScanner(bytes.NewReader(complete))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if text, ok := parseCodexPrompt(sc.Bytes()); ok {
			emit(agent, text)
		}
	}
	return offset + int64(len(complete))
}

// codexLine is the subset of a codex rollout line we inspect.
type codexLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		CWD     string `json:"cwd"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

// parseCodexPrompt returns the prompt text if line is a real interactive user
// prompt: type=response_item, payload.type=message, payload.role=user, with
// an input_text content item whose text isn't a harness-injected wrapper
// (codex prepends <environment_context>, <permissions instructions>, etc. on
// every turn — those always start with `<`).
func parseCodexPrompt(line []byte) (string, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", false
	}
	var cl codexLine
	if err := json.Unmarshal(line, &cl); err != nil {
		return "", false
	}
	if cl.Type != "response_item" || cl.Payload.Type != "message" || cl.Payload.Role != "user" {
		return "", false
	}
	for _, c := range cl.Payload.Content {
		if c.Type != "input_text" {
			continue
		}
		text := strings.TrimSpace(c.Text)
		if text == "" || strings.HasPrefix(text, "<") {
			continue
		}
		return text, true
	}
	return "", false
}
