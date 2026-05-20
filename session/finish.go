package session

import (
	"fmt"
	"os"
	"os/exec"

	"claude-squad/log"
	"claude-squad/session/journal"
)

// FinishOptions are the inputs to CreateFinish. The package-level function
// runs both in-process (the TUI) and from the `cs finish` subprocess, so all
// inputs are passed explicitly rather than read off an *Instance.
type FinishOptions struct {
	JournalPath  string             // path to the session's journal.jsonl
	Session      journal.SessionRef // session id + title
	Workspace    string             // workspace id
	WorktreePath string             // git worktree; cwd for git commands ("" skips git)
	Agent        journal.AgentRef   // who is recording the finish
	Finish       journal.Finish     // the required-five payload; validated here
}

// CreateFinish validates the closeout payload, appends a finish event to the
// session journal, and mirrors a one-line marker to a git note on HEAD.
// Validation runs before any side effect — a malformed finish never lands on
// disk, mirroring miagent's `miagent-task finish` gate.
func CreateFinish(opts FinishOptions) error {
	if opts.JournalPath == "" {
		return fmt.Errorf("finish: journaling is not enabled for this session")
	}
	if err := opts.Finish.Validate(); err != nil {
		return err
	}

	j, err := journal.Reopen(opts.JournalPath, opts.Session, opts.Workspace)
	if err != nil {
		return fmt.Errorf("finish: %w", err)
	}
	defer j.Close()

	agent := opts.Agent
	if agent.Name == "" {
		agent.Name = journal.AgentHuman
	}
	if err := j.Append(journal.FinishEvent(agent, opts.Finish)); err != nil {
		return fmt.Errorf("finish: append event: %w", err)
	}

	// Mirror to a git note so the closeout is visible from the commit graph.
	// Best-effort; the journal is the canonical record.
	if opts.WorktreePath != "" {
		if sha := gitHeadSHA(opts.WorktreePath); sha != "" {
			writeFinishNote(opts.WorktreePath, opts.Finish)
		}
	}
	return nil
}

// Finish records a finish event for this session. Thin wrapper over the
// package-level CreateFinish.
func (i *Instance) Finish(f journal.Finish) error {
	path, err := i.journalPath()
	if err != nil {
		return err
	}
	worktree := ""
	if i.gitWorktree != nil {
		worktree = i.gitWorktree.GetWorktreePath()
	}
	return CreateFinish(FinishOptions{
		JournalPath:  path,
		Session:      journal.SessionRef{ID: i.SessionID, Title: i.Title},
		Workspace:    i.WorkspaceID,
		WorktreePath: worktree,
		Agent:        journal.AgentRef{Name: journal.AgentHuman},
		Finish:       f,
	})
}

// FinishFromEnv records a finish for the session the caller is running inside,
// resolving it from the CS_* environment variables claude-squad injects into
// every session. This is the entry point for `cs finish`.
func FinishFromEnv(f journal.Finish) error {
	journalPath := os.Getenv(EnvJournalPath)
	if journalPath == "" {
		return fmt.Errorf(
			"not inside a claude-squad session with journaling enabled (%s is unset)", EnvJournalPath)
	}
	return CreateFinish(FinishOptions{
		JournalPath:  journalPath,
		Session:      journal.SessionRef{ID: os.Getenv(EnvSessionID), Title: os.Getenv(EnvSession)},
		Workspace:    os.Getenv(EnvWorkspace),
		WorktreePath: os.Getenv(EnvWorktreePath),
		Agent:        journal.AgentRef{Name: journal.AgentHuman},
		Finish:       f,
	})
}

// writeFinishNote attaches a short finish marker to HEAD under
// refs/notes/cs/finish. Best-effort: failures are logged, not returned.
func writeFinishNote(worktree string, f journal.Finish) {
	msg := fmt.Sprintf("cs-finish disposition=%s verification=%s\n\n%s",
		f.Disposition, f.Verification.Status, f.Intent)
	cmd := exec.Command("git", "-C", worktree,
		"notes", "--ref=cs/finish", "add", "-f", "-m", msg, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.WarningLog.Printf("finish git note: %v\n%s", err, out)
	}
}
