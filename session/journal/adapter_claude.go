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

// claudePollInterval is how often the adapter re-scans the transcript dir.
const claudePollInterval = time.Second

// ClaudeAdapter watches claude-code's own JSONL transcript files for a worktree
// and reports real user prompts. claude-code writes transcripts to
// <configDir>/projects/<encoded-cwd>/*.jsonl.
type ClaudeAdapter struct {
	transcriptDir string
	version       string
	pollInterval  time.Duration
}

// NewClaudeAdapter builds an adapter for a claude-code session running in
// worktree cwd. configDir is the resolved CLAUDE_CONFIG_DIR (default ~/.claude);
// version is the claude-code CLI version if known, else "".
func NewClaudeAdapter(configDir, cwd, version string) *ClaudeAdapter {
	return &ClaudeAdapter{
		transcriptDir: filepath.Join(configDir, "projects", EncodeProjectPath(cwd)),
		version:       version,
		pollInterval:  claudePollInterval,
	}
}

// EncodeProjectPath maps a filesystem path to claude-code's transcript
// subdirectory name: every non-alphanumeric character becomes '-'.
// e.g. /Users/foo/bar.git -> -Users-foo-bar-git
func EncodeProjectPath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Run implements Adapter. It polls the transcript dir on a ticker, draining new
// lines from every *.jsonl file and emitting the ones that are real prompts.
func (a *ClaudeAdapter) Run(ctx context.Context, emit func(AgentRef, string)) {
	agent := AgentRef{Name: AgentClaudeCode, Version: a.version}

	// offsets tracks bytes already consumed per transcript file. Files that
	// exist when Run starts are seeded to EOF (tail-only — never replay
	// history); files that appear later are new conversations, read from 0.
	offsets := map[string]int64{}
	for _, f := range a.listTranscripts() {
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
			for _, f := range a.listTranscripts() {
				offsets[f] = a.drain(f, offsets[f], agent, emit)
			}
		}
	}
}

func (a *ClaudeAdapter) listTranscripts() []string {
	entries, err := os.ReadDir(a.transcriptDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, filepath.Join(a.transcriptDir, e.Name()))
		}
	}
	return out
}

// drain reads complete newline-terminated lines from path starting at offset,
// emits any that are real user prompts, and returns the new offset. The offset
// advances only past complete lines, so a half-written trailing line is re-read
// on the next tick rather than parsed truncated.
func (a *ClaudeAdapter) drain(path string, offset int64, agent AgentRef, emit func(AgentRef, string)) int64 {
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
		offset = 0 // file was truncated or rotated — restart
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
		return offset // no complete line yet
	}
	complete := data[:lastNL+1]

	sc := bufio.NewScanner(bytes.NewReader(complete))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript lines can be large
	for sc.Scan() {
		if text, ok := parseClaudePrompt(sc.Bytes()); ok {
			emit(agent, text)
		}
	}
	return offset + int64(len(complete))
}

// claudeLine is the subset of a claude-code transcript line we inspect.
type claudeLine struct {
	Type        string `json:"type"`
	UserType    string `json:"userType"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// parseClaudePrompt returns the prompt text if line is a real interactive user
// prompt: type=user, userType=external, not a sidechain, and message.content is
// a plain string. Array/object content is tool_result or image output — the
// model's turn machinery, not something the user typed — and is rejected.
func parseClaudePrompt(line []byte) (string, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", false
	}
	var cl claudeLine
	if err := json.Unmarshal(line, &cl); err != nil {
		return "", false
	}
	if cl.Type != "user" || cl.UserType != "external" || cl.IsSidechain {
		return "", false
	}
	c := bytes.TrimSpace(cl.Message.Content)
	if len(c) == 0 || c[0] != '"' {
		return "", false // not a JSON string -> tool_result/image content
	}
	var text string
	if err := json.Unmarshal(c, &text); err != nil {
		return "", false
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", false
	}
	return text, true
}
