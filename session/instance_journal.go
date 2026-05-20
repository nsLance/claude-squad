package session

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session/journal"
	"claude-squad/session/tmux"
)

// CS_* environment variables exported into every session's tmux environment so
// tools running inside the pane — notably `cs checkpoint` — can locate the
// session and its journal. CS_REPO_PATH/WORKTREE_PATH/BRANCH/SESSION are also
// the post-worktree hook contract (#260).
const (
	EnvRepoPath     = "CS_REPO_PATH"
	EnvWorktreePath = "CS_WORKTREE_PATH"
	EnvBranch       = "CS_BRANCH"
	EnvSession      = "CS_SESSION"    // session title
	EnvSessionID    = "CS_SESSION_ID" // session UUID
	EnvWorkspace    = "CS_WORKSPACE"  // workspace id
	EnvJournalPath  = "CS_JOURNAL"    // journal.jsonl path; empty when journaling is disabled
)

// journalDirSanitizer matches runs of characters not allowed in the session's
// on-disk directory name.
var journalDirSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// adapterStopTimeout bounds how long stopJournal waits for the adapter
// goroutine to exit before closing the journal out from under it.
const adapterStopTimeout = 2 * time.Second

// injectCSEnv mints the session id if needed and appends the CS_* variables to
// env. It is applied to the env handed to every tmux session start, so the
// values reach the pane whether or not journaling is enabled.
func (i *Instance) injectCSEnv(env []string) []string {
	if i.SessionID == "" {
		i.SessionID = journal.NewSessionID()
	}
	var worktree, repo string
	if i.gitWorktree != nil {
		worktree = i.gitWorktree.GetWorktreePath()
		repo = i.gitWorktree.GetRepoPath()
	}
	jp, _ := i.journalPath()
	return append(env,
		EnvRepoPath+"="+repo,
		EnvWorktreePath+"="+worktree,
		EnvBranch+"="+i.Branch,
		EnvSession+"="+i.Title,
		EnvSessionID+"="+i.SessionID,
		EnvWorkspace+"="+i.WorkspaceID,
		EnvJournalPath+"="+jp,
	)
}

// journalPath returns the on-disk path of this session's journal:
//
//	<workspace-dir>/sessions/<sanitized-title>-<short-session-id>/journal.jsonl
//
// It returns "" (no error) when the instance has no workspace or session id —
// journaling is workspace-scoped and silently disabled outside one.
func (i *Instance) journalPath() (string, error) {
	if i.WorkspaceID == "" || i.SessionID == "" {
		return "", nil
	}
	ws := config.LoadWorkspaceRegistry().Get(i.WorkspaceID)
	if ws == nil {
		return "", nil
	}
	wsDir, err := ws.Dir()
	if err != nil {
		return "", err
	}
	short := i.SessionID
	if len(short) > 8 {
		short = short[:8]
	}
	name := strings.Trim(journalDirSanitizer.ReplaceAllString(i.Title, "-"), "-")
	if name == "" {
		name = "session"
	}
	return filepath.Join(wsDir, "sessions", name+"-"+short, "journal.jsonl"), nil
}

// worktreeJournalLink is the stable path inside the worktree that symlinks to
// the session's journal, so agents and tools find it at a predictable location.
func (i *Instance) worktreeJournalLink() string {
	if i.gitWorktree == nil {
		return ""
	}
	return filepath.Join(i.gitWorktree.GetWorktreePath(), ".cs", "journal.jsonl")
}

// transcriptPath returns the on-disk path for the raw pane transcript, a
// sibling of the journal at <session-dir>/transcript.raw. Empty when
// journaling is disabled (no workspace or no session id).
func (i *Instance) transcriptPath() (string, error) {
	jp, err := i.journalPath()
	if err != nil || jp == "" {
		return jp, err
	}
	return filepath.Join(filepath.Dir(jp), "transcript.raw"), nil
}

// startJournal opens the journal, launches the agent-specific transcript
// adapter, and turns on the LLM-agnostic pipe-pane safety net. Called once a
// session's tmux pane is up. Best-effort: failures are logged as warnings and
// never break the session.
func (i *Instance) startJournal() {
	i.ensureJournal()
	i.startAdapter()
	i.startTranscript()
}

// ensureJournal opens (creating if needed) this session's journal, writes the
// header on a fresh file, and symlinks it into the worktree at .cs/journal.jsonl.
// No-op without a workspace, or if the journal is already open.
func (i *Instance) ensureJournal() {
	if i.journal != nil {
		return
	}
	path, err := i.journalPath()
	if err != nil {
		log.WarningLog.Printf("journal path: %v", err)
		return
	}
	if path == "" {
		return // journaling disabled (no workspace)
	}
	j, isNew, err := journal.Open(path, journal.SessionRef{ID: i.SessionID, Title: i.Title}, i.WorkspaceID)
	if err != nil {
		log.WarningLog.Printf("open journal: %v", err)
		return
	}
	if isNew {
		if err := j.Append(journal.HeaderEvent(config.Version)); err != nil {
			log.WarningLog.Printf("write journal header: %v", err)
		}
	}
	i.journal = j
	i.linkJournalIntoWorktree(path)
}

func (i *Instance) linkJournalIntoWorktree(target string) {
	link := i.worktreeJournalLink()
	if link == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		log.WarningLog.Printf("journal symlink dir: %v", err)
		return
	}
	_ = os.Remove(link) // replace any stale link
	if err := os.Symlink(target, link); err != nil {
		log.WarningLog.Printf("journal symlink: %v", err)
	}
}

// startAdapter launches the transcript adapter for this session's agent, if one
// exists, so real user prompts land in the journal automatically. No-op when
// journaling is off or the agent has no adapter.
func (i *Instance) startAdapter() {
	if i.journal == nil || i.adapterCancel != nil || i.gitWorktree == nil {
		return
	}
	var adapter journal.Adapter
	switch {
	case strings.HasSuffix(i.Program, tmux.ProgramClaude):
		env, _ := i.resolveEnv()
		adapter = journal.NewClaudeAdapter(claudeConfigDir(env), i.gitWorktree.GetWorktreePath(), "")
	default:
		return // no adapter for this agent yet
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	j := i.journal
	go func() {
		adapter.Run(ctx, func(agent journal.AgentRef, text string) {
			if err := j.Append(journal.PromptEvent(agent, text)); err != nil {
				log.WarningLog.Printf("journal prompt: %v", err)
			}
		})
		close(done)
	}()
	i.adapterCancel = cancel
	i.adapterDone = done
}

// startTranscript turns on tmux pipe-pane to <session-dir>/transcript.raw so
// every byte rendered to the pane is captured regardless of which CLI is
// running. Co-scoped with the journal: no workspace, no transcript.
func (i *Instance) startTranscript() {
	if i.tmuxSession == nil {
		return
	}
	path, err := i.transcriptPath()
	if err != nil {
		log.WarningLog.Printf("transcript path: %v", err)
		return
	}
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.WarningLog.Printf("transcript dir: %v", err)
		return
	}
	if err := i.tmuxSession.PipePane(path); err != nil {
		log.WarningLog.Printf("start transcript: %v", err)
	}
}

// stopTranscript closes the pipe-pane. Must be called while the tmux session
// is still alive — i.e. before Close/DetachSafely in the parent.
func (i *Instance) stopTranscript() {
	if i.tmuxSession == nil {
		return
	}
	if err := i.tmuxSession.StopPipePane(); err != nil {
		log.WarningLog.Printf("stop transcript: %v", err)
	}
}

// stopJournal stops the adapter, closes the journal, and removes the worktree
// symlink. Called on Pause and Kill. Best-effort and idempotent.
func (i *Instance) stopJournal() {
	if i.adapterCancel != nil {
		i.adapterCancel()
		i.adapterCancel = nil
		if i.adapterDone != nil {
			select {
			case <-i.adapterDone:
			case <-time.After(adapterStopTimeout):
			}
			i.adapterDone = nil
		}
	}
	i.stopTranscript()
	if i.journal != nil {
		if err := i.journal.Close(); err != nil {
			log.WarningLog.Printf("close journal: %v", err)
		}
		i.journal = nil
	}
	if link := i.worktreeJournalLink(); link != "" {
		_ = os.Remove(link)
	}
}

// claudeConfigDir resolves claude-code's config dir: CLAUDE_CONFIG_DIR from the
// session env if set, else ~/.claude.
func claudeConfigDir(env []string) string {
	const key = "CLAUDE_CONFIG_DIR="
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key); ok && v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}
