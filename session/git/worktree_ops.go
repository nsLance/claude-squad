package git

import (
	"claude-squad/log"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// BranchCollisionError is returned when `git worktree add` refuses because the
// requested branch is already checked out by another worktree (typically an
// orphan left behind by a previous session). Callers can use errors.As to pull
// out the conflicting path and prompt the user to clean it up.
type BranchCollisionError struct {
	Branch       string
	WorktreePath string
}

func (e *BranchCollisionError) Error() string {
	return fmt.Sprintf("branch %q is already used by worktree at %q (likely an orphan from a previous session — remove it with `git worktree remove --force %s` or pick a different session title)",
		e.Branch, e.WorktreePath, e.WorktreePath)
}

// branchCollisionRE matches the line git prints when refusing to reuse a
// branch that another worktree already has checked out. The path is captured.
var branchCollisionRE = regexp.MustCompile(`is already used by worktree at '([^']+)'`)

// classifyWorktreeAddErr inspects a `git worktree add` failure and, if it
// matches the branch-collision pattern, returns a typed error so the TUI can
// render a useful message instead of raw git stderr.
func classifyWorktreeAddErr(branch string, err error) error {
	if err == nil {
		return nil
	}
	if m := branchCollisionRE.FindStringSubmatch(err.Error()); m != nil {
		return &BranchCollisionError{Branch: branch, WorktreePath: m[1]}
	}
	return err
}

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() error {
	// Ensure worktrees directory exists early (can be done in parallel with branch check)
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return err
	}

	// If this worktree uses a pre-existing branch, always set up from that branch
	// (it may exist locally or only on the remote).
	if g.isExistingBranch {
		return g.setupFromExistingBranch()
	}

	// Check if branch exists using git CLI (much faster than go-git PlainOpen)
	_, err = g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if err == nil {
		return g.setupFromExistingBranch()
	}
	return g.setupNewWorktree()
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("branch %s not found locally or on remote", g.branchName)
		}
		// Create a local tracking branch via worktree add -b
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, fmt.Sprintf("origin/%s", g.branchName)); err != nil {
			return classifyWorktreeAddErr(g.branchName, err)
		}
		return nil
	}

	// Create a new worktree from the existing local branch
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
		return classifyWorktreeAddErr(g.branchName, err)
	}

	return nil
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen)
	_, _ = g.runGitCommand(g.repoPath, "branch", "-D", g.branchName) // Ignore error if branch doesn't exist

	output, err := g.runGitCommand(g.repoPath, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "fatal: ambiguous argument 'HEAD'") ||
			strings.Contains(err.Error(), "fatal: not a valid object name") ||
			strings.Contains(err.Error(), "fatal: HEAD: not a valid object name") {
			return fmt.Errorf("this appears to be a brand new repository: please create an initial commit before creating an instance")
		}
		return fmt.Errorf("failed to get HEAD commit hash: %w", err)
	}
	headCommit := strings.TrimSpace(string(output))
	g.baseCommitSHA = headCommit

	// Create a new worktree from the HEAD commit
	// Otherwise, we'll inherit uncommitted changes from the previous worktree.
	// This way, we can start the worktree with a clean slate.
	// TODO: we might want to give an option to use main/master instead of the current branch.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, headCommit); err != nil {
		return classifyWorktreeAddErr(g.branchName, err)
	}

	return nil
}

// Cleanup removes the worktree and associated branch
func (g *GitWorktree) Cleanup() error {
	var errs []error

	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, but skip if this is a pre-existing branch
	if !g.isExistingBranch {
		if _, err := g.runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil {
			// Only log if it's not a "branch not found" error
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, fmt.Errorf("failed to remove branch %s: %w", g.branchName, err))
			}
		}
	}

	// Prune the worktree to clean up any remaining references
	if err := g.Prune(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return g.combineErrors(errs)
	}

	return nil
}

// Remove removes the worktree but keeps the branch
func (g *GitWorktree) Remove() error {
	// Remove the worktree using git command
	if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// Prune removes all working tree administrative files and directories
func (g *GitWorktree) Prune() error {
	if _, err := g.runGitCommand(g.repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}
	return nil
}

// CleanupWorktrees removes the legacy pre-workspaces worktree directory
// ($CLAUDE_SQUAD_HOME/worktrees) and best-effort deletes any branches the
// directories were associated with. Post-workspaces every active worktree
// lives under workspaces/<id>/worktrees/ — those are handled by
// CleanupWorkspaceWorktrees. This only mops up what the pre-workspaces build
// left behind.
//
// Tolerates being invoked from outside a git repo: `cs reset` can now be run
// anywhere (per the workspace launch flow), so the `git worktree list` step
// — which depends on cwd being a repo — is best-effort and never fails the
// caller. Directory removal still happens regardless.
func CleanupWorktrees() error {
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	entries, err := os.ReadDir(worktreesDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read worktree directory: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	// Best-effort branch lookup. Without -C, this runs in cwd; when cs reset
	// is invoked outside a git repo (legitimate post-workspaces), git exits
	// 128. That's expected — we just lose the branch-deletion shortcut and
	// fall through to dir removal. The branches will dangle in their original
	// repos, but those repos are the workspaces' RepoPath which the
	// per-workspace cleanup already handles for current sessions.
	worktreeBranches := make(map[string]string)
	if output, err := exec.Command("git", "worktree", "list", "--porcelain").Output(); err == nil {
		currentWorktree := ""
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "worktree ") {
				currentWorktree = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch ") {
				branchName := strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
				if currentWorktree != "" {
					worktreeBranches[currentWorktree] = branchName
				}
			}
		}
	} else {
		log.WarningLog.Printf("legacy worktree cleanup: skipping branch lookup (git worktree list: %v)", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			worktreePath := filepath.Join(worktreesDir, entry.Name())

			for path, branch := range worktreeBranches {
				if strings.Contains(path, entry.Name()) {
					deleteCmd := exec.Command("git", "branch", "-D", branch)
					if err := deleteCmd.Run(); err != nil {
						log.ErrorLog.Printf("failed to delete branch %s: %v", branch, err)
					}
					break
				}
			}

			os.RemoveAll(worktreePath)
		}
	}

	// Prune is also best-effort for the same reason: requires cwd to be a repo.
	if _, err := exec.Command("git", "worktree", "prune").Output(); err != nil {
		log.WarningLog.Printf("legacy worktree cleanup: skipping prune (%v)", err)
	}

	return nil
}

// CleanupWorkspaceWorktrees removes every worktree whose path is under
// worktreeRoot from the git repo at repoPath, deletes the associated branches,
// and prunes. This is the per-workspace counterpart to CleanupWorktrees, which
// only handles the legacy global worktree directory. Returns a combined error
// if any per-worktree removal failed, but always attempts every one — partial
// progress beats abort-on-first-error here.
func CleanupWorkspaceWorktrees(repoPath, worktreeRoot string) error {
	if repoPath == "" || worktreeRoot == "" {
		return nil
	}
	// If the worktree root doesn't exist, there's nothing to clean.
	if _, err := os.Stat(worktreeRoot); os.IsNotExist(err) {
		return nil
	}

	listCmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("list worktrees in %s: %w", repoPath, err)
	}

	type entry struct{ path, branch string }
	var entries []entry
	var cur entry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = entry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			flush()
		}
	}
	flush()

	// Normalize worktreeRoot for the prefix check. macOS resolves $TMPDIR
	// (and similar) through symlinks before `git worktree list` records the
	// path, so an unresolved root would fail to match git's canonicalized
	// entries even when both point at the same directory.
	rootResolved, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		rootResolved = worktreeRoot
	}
	root := strings.TrimRight(rootResolved, string(filepath.Separator)) + string(filepath.Separator)

	var errs []error
	for _, e := range entries {
		entryPath := e.path
		if resolved, rerr := filepath.EvalSymlinks(entryPath); rerr == nil {
			entryPath = resolved
		}
		if !strings.HasPrefix(entryPath+string(filepath.Separator), root) {
			continue
		}
		if _, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "-f", e.path).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("remove worktree %s: %w", e.path, err))
		}
		if e.branch != "" {
			// Branch deletion is best-effort: a missing branch just means git
			// already cleaned it up alongside the worktree.
			if out, err := exec.Command("git", "-C", repoPath, "branch", "-D", e.branch).CombinedOutput(); err != nil {
				if !strings.Contains(string(out), "not found") {
					log.WarningLog.Printf("delete branch %s in %s: %v", e.branch, repoPath, err)
				}
			}
		}
	}

	if _, err := exec.Command("git", "-C", repoPath, "worktree", "prune").CombinedOutput(); err != nil {
		errs = append(errs, fmt.Errorf("prune worktrees in %s: %w", repoPath, err))
	}

	// Sweep any leftover directories that git didn't know about (e.g. a worktree
	// removed manually but whose dir survived).
	if err := os.RemoveAll(worktreeRoot); err != nil {
		errs = append(errs, fmt.Errorf("remove worktree root %s: %w", worktreeRoot, err))
	}

	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("workspace worktree cleanup: %s", strings.Join(msgs, "; "))
}

// RemoveOrphanWorktree force-removes a single worktree at worktreePath from
// the git repo at repoPath, deletes the branch (best-effort), and prunes the
// repo's worktree registry. Used by the TUI's orphan-cleanup confirmation
// overlay after a BranchCollisionError.
func RemoveOrphanWorktree(repoPath, worktreePath, branch string) error {
	if out, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "-f", worktreePath).CombinedOutput(); err != nil {
		return fmt.Errorf("remove worktree %s: %s: %w", worktreePath, strings.TrimSpace(string(out)), err)
	}
	if branch != "" {
		if out, err := exec.Command("git", "-C", repoPath, "branch", "-D", branch).CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "not found") {
				return fmt.Errorf("delete branch %s: %s: %w", branch, strings.TrimSpace(string(out)), err)
			}
		}
	}
	if out, err := exec.Command("git", "-C", repoPath, "worktree", "prune").CombinedOutput(); err != nil {
		return fmt.Errorf("prune worktrees in %s: %s: %w", repoPath, strings.TrimSpace(string(out)), err)
	}
	return nil
}
