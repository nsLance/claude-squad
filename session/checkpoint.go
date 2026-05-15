package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"claude-squad/log"
	"claude-squad/session/journal"
)

// CheckpointOptions are the inputs to CreateCheckpoint. The package-level
// function runs both in-process (the TUI) and from the `cs checkpoint`
// subprocess, so everything it needs is passed explicitly rather than read off
// an *Instance.
type CheckpointOptions struct {
	JournalPath  string             // path to the session's journal.jsonl
	Session      journal.SessionRef // session id + title
	Workspace    string             // workspace id
	WorktreePath string             // git worktree; cwd for git commands ("" skips git)
	Summary      string             // one-line checkpoint summary
	Agent        journal.AgentRef   // who is recording the checkpoint
}

// CreateCheckpoint appends a signed checkpoint event to the session journal and
// mirrors the signature to a git note on HEAD. The signature chains this
// checkpoint to the previous one (journal.ComputeSignature over the journal
// bytes written since), so tampering with any earlier byte is detectable.
// Returns the new Signature.
func CreateCheckpoint(opts CheckpointOptions) (*journal.Signature, error) {
	if opts.JournalPath == "" {
		return nil, fmt.Errorf("checkpoint: journaling is not enabled for this session")
	}
	if strings.TrimSpace(opts.Summary) == "" {
		return nil, fmt.Errorf("checkpoint: a summary is required")
	}

	// prev hash and start offset come from the previous checkpoint in the
	// chain; both zero values for the first checkpoint.
	var prev string
	var from int64
	if last, err := journal.LastCheckpoint(opts.JournalPath); err != nil {
		return nil, fmt.Errorf("checkpoint: read journal: %w", err)
	} else if last != nil {
		prev, from = last.Hash, last.To
	}

	j, err := journal.Reopen(opts.JournalPath, opts.Session, opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	defer j.Close()

	to, err := j.Size()
	if err != nil {
		return nil, fmt.Errorf("checkpoint: journal size: %w", err)
	}
	rangeBytes, err := j.ReadRange(from, to)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read journal range: %w", err)
	}

	gitSHA := gitHeadSHA(opts.WorktreePath)
	sig := &journal.Signature{
		Prev: prev,
		Hash: journal.ComputeSignature(prev, rangeBytes, opts.Summary, gitSHA),
		From: from,
		To:   to,
	}

	agent := opts.Agent
	if agent.Name == "" {
		agent.Name = journal.AgentHuman
	}
	if err := j.Append(journal.Event{
		Type:      journal.TypeCheckpoint,
		Agent:     agent,
		Summary:   opts.Summary,
		GitSHA:    gitSHA,
		Signature: sig,
	}); err != nil {
		return nil, fmt.Errorf("checkpoint: append event: %w", err)
	}

	// Mirror to a git note — an audit aid; the journal is the canonical record.
	if opts.WorktreePath != "" && gitSHA != "" {
		writeCheckpointNote(opts.WorktreePath, sig.Hash, opts.Summary)
	}
	return sig, nil
}

// CreateCheckpoint records a signed checkpoint for this session. Thin wrapper
// over the package-level CreateCheckpoint: it supplies the session's journal
// path, identity, and worktree.
func (i *Instance) CreateCheckpoint(summary string) (*journal.Signature, error) {
	path, err := i.journalPath()
	if err != nil {
		return nil, err
	}
	worktree := ""
	if i.gitWorktree != nil {
		worktree = i.gitWorktree.GetWorktreePath()
	}
	return CreateCheckpoint(CheckpointOptions{
		JournalPath:  path,
		Session:      journal.SessionRef{ID: i.SessionID, Title: i.Title},
		Workspace:    i.WorkspaceID,
		WorktreePath: worktree,
		Summary:      summary,
		Agent:        journal.AgentRef{Name: journal.AgentHuman},
	})
}

// CheckpointFromEnv records a checkpoint for the session the caller is running
// inside, resolving it from the CS_* environment variables claude-squad injects
// into every session. This is the entry point for the `cs checkpoint`
// subprocess, which runs in the session's tmux pane and inherits that env.
func CheckpointFromEnv(summary string) (*journal.Signature, error) {
	journalPath := os.Getenv(EnvJournalPath)
	if journalPath == "" {
		return nil, fmt.Errorf(
			"not inside a claude-squad session with journaling enabled (%s is unset)", EnvJournalPath)
	}
	return CreateCheckpoint(CheckpointOptions{
		JournalPath:  journalPath,
		Session:      journal.SessionRef{ID: os.Getenv(EnvSessionID), Title: os.Getenv(EnvSession)},
		Workspace:    os.Getenv(EnvWorkspace),
		WorktreePath: os.Getenv(EnvWorktreePath),
		Summary:      summary,
		Agent:        journal.AgentRef{Name: journal.AgentHuman},
	})
}

// gitHeadSHA returns the worktree's HEAD commit SHA, or "" if it can't be read
// (no commits yet, not a repo). Best-effort: the checkpoint still signs without
// it — the SHA is just one more input to the hash.
func gitHeadSHA(worktree string) string {
	if worktree == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeCheckpointNote attaches the checkpoint hash and summary to HEAD under
// refs/notes/cs/checkpoints. Best-effort: failures are logged, not returned.
func writeCheckpointNote(worktree, hash, summary string) {
	msg := fmt.Sprintf("cs-checkpoint %s\n\n%s", hash, summary)
	cmd := exec.Command("git", "-C", worktree,
		"notes", "--ref=cs/checkpoints", "add", "-f", "-m", msg, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.WarningLog.Printf("checkpoint git note: %v\n%s", err, out)
	}
}
