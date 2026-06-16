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

// worktreeEntry is a single line group from `git worktree list --porcelain`.
type worktreeEntry struct{ path, branch string }

// listWorktreeEntries runs `git worktree list --porcelain` in repoPath and
// parses the registered worktrees (path + branch, branch without the
// refs/heads/ prefix).
func listWorktreeEntries(repoPath string) ([]worktreeEntry, error) {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("list worktrees in %s: %w", repoPath, err)
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
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
	return entries, nil
}

// resolveRoots EvalSymlinks-resolves each non-empty root and slash-terminates
// it, so HasPrefix checks line up with git's canonicalized porcelain paths
// (macOS resolves $TMPDIR and similar through symlinks).
func resolveRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		resolved := r
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			resolved = rr
		}
		out = append(out, strings.TrimRight(resolved, string(filepath.Separator))+string(filepath.Separator))
	}
	return out
}

// resolvePath returns the symlink-resolved path, falling back to the input when
// resolution fails (e.g. the path no longer exists on disk).
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// isCSManagedWorktree reports whether a worktree (path + branch, branch without
// the refs/heads/ prefix) is one claude-squad created and may safely tear down.
// True if EITHER the resolved path is under a known cs root OR the branch
// carries branchPrefix. roots must already be resolved + slash-terminated (see
// resolveRoots). An empty branchPrefix disables the branch signal so a
// prefix-less misconfiguration doesn't classify every worktree as cs-managed.
func isCSManagedWorktree(worktreePath, branch string, roots []string, branchPrefix string) bool {
	p := resolvePath(worktreePath) + string(filepath.Separator)
	for _, root := range roots {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	if branchPrefix != "" && branch != "" && strings.HasPrefix(branch, branchPrefix) {
		return true
	}
	return false
}

// OrphanWorktree is a cs worktree under a root with no backing live session.
type OrphanWorktree struct {
	Path   string // the worktree path as git/disk knows it (unresolved)
	Branch string // associated branch, "" for a dangling directory git forgot
}

// collectOrphans returns every cs worktree under worktreeRoot in repoPath whose
// resolved path is not in keep: both git-registered worktrees (the crash case —
// git's registry survives but the session record didn't) and dangling
// worktree directories git no longer tracks (a partial removal). keep holds
// EvalSymlinks-resolved live-session worktree paths.
func collectOrphans(repoPath, worktreeRoot string, keep map[string]struct{}) ([]OrphanWorktree, error) {
	entries, listErr := listWorktreeEntries(repoPath)

	root := strings.TrimRight(resolvePath(worktreeRoot), string(filepath.Separator)) + string(filepath.Separator)

	registered := make(map[string]struct{})
	var orphans []OrphanWorktree
	for _, e := range entries {
		entryResolved := resolvePath(e.path)
		if !strings.HasPrefix(entryResolved+string(filepath.Separator), root) {
			continue
		}
		registered[entryResolved] = struct{}{}
		if _, ok := keep[entryResolved]; ok {
			continue
		}
		orphans = append(orphans, OrphanWorktree{Path: e.path, Branch: e.branch})
	}

	// Sweep dangling worktree directories git no longer tracks. Worktree paths
	// nest by branch namespace (<root>/nakkul/cs-edge_<nanos>), so we walk and
	// treat any directory containing a .git entry as a worktree dir — never the
	// intermediate namespace dirs that may still hold a kept worktree.
	_ = filepath.WalkDir(worktreeRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == worktreeRoot {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr != nil {
			return nil // not a worktree dir; keep descending
		}
		resolved := resolvePath(path)
		if _, ok := keep[resolved]; ok {
			return filepath.SkipDir
		}
		if _, ok := registered[resolved]; ok {
			return filepath.SkipDir // already handled via the registry above
		}
		orphans = append(orphans, OrphanWorktree{Path: path})
		return filepath.SkipDir
	})

	return orphans, listErr
}

// tearDownWorktree force-removes a single worktree and, when branch is
// non-empty, best-effort deletes that branch. If `git worktree remove` fails
// (e.g. the dir is dangling and git no longer tracks it), it falls back to
// removing the directory outright. Returns collected (non-fatal) errors.
func tearDownWorktree(repoPath, path, branch string) []error {
	var errs []error
	if out, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "-f", path).CombinedOutput(); err != nil {
		if rmErr := os.RemoveAll(path); rmErr != nil {
			errs = append(errs, fmt.Errorf("remove worktree %s: %s: %w", path, strings.TrimSpace(string(out)), err))
		}
	}
	if branch != "" {
		// Best-effort: a missing branch just means git already cleaned it up.
		if out, err := exec.Command("git", "-C", repoPath, "branch", "-D", branch).CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "not found") {
				log.WarningLog.Printf("delete branch %s in %s: %v", branch, repoPath, err)
			}
		}
	}
	return errs
}

// joinErrs combines best-effort errors under a single prefixed message, or nil.
func joinErrs(prefix string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s: %s", prefix, strings.Join(msgs, "; "))
}

// reconcile is the shared core behind CleanupWorkspaceWorktrees and
// ReconcileWorktrees. It removes every cs worktree under worktreeRoot except
// those in keep, prunes the registry, and (when nukeRoot) removes the whole
// root. A branch is deleted only when forceBranchDelete is set or it carries
// branchPrefix — otherwise it's left intact (it may be a pre-existing branch a
// session merely borrowed). Always attempts every orphan; partial progress
// beats abort-on-first-error.
func reconcile(repoPath, worktreeRoot, branchPrefix string, keep map[string]struct{}, nukeRoot, forceBranchDelete bool) error {
	if repoPath == "" || worktreeRoot == "" {
		return nil
	}
	if _, err := os.Stat(worktreeRoot); os.IsNotExist(err) {
		return nil
	}

	orphans, listErr := collectOrphans(repoPath, worktreeRoot, keep)
	var errs []error
	if listErr != nil {
		errs = append(errs, listErr)
	}
	for _, o := range orphans {
		branch := ""
		if o.Branch != "" && (forceBranchDelete || (branchPrefix != "" && strings.HasPrefix(o.Branch, branchPrefix))) {
			branch = o.Branch
		}
		errs = append(errs, tearDownWorktree(repoPath, o.Path, branch)...)
	}

	if _, err := exec.Command("git", "-C", repoPath, "worktree", "prune").CombinedOutput(); err != nil {
		errs = append(errs, fmt.Errorf("prune worktrees in %s: %w", repoPath, err))
	}

	if nukeRoot {
		if err := os.RemoveAll(worktreeRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove worktree root %s: %w", worktreeRoot, err))
		}
	}

	return joinErrs("reconcile worktrees", errs)
}

// CleanupWorkspaceWorktrees removes every worktree under worktreeRoot from the
// git repo at repoPath, deletes the associated branches, prunes, and removes the
// whole root. Used by `cs reset`, so it is intentionally aggressive (no keep-set,
// unconditional branch deletion). The per-workspace counterpart to
// CleanupWorktrees, which only handles the legacy global worktree directory.
func CleanupWorkspaceWorktrees(repoPath, worktreeRoot string) error {
	return reconcile(repoPath, worktreeRoot, "", nil, true /*nukeRoot*/, true /*forceBranchDelete*/)
}

// ListOrphanWorktrees returns the cs worktrees under worktreeRoot in repoPath
// not backed by a live session (keep holds resolved live worktree paths). Used
// by `cs gc` to preview what a reconcile would remove. Returns nil when the root
// is absent.
func ListOrphanWorktrees(repoPath, worktreeRoot string, keep map[string]struct{}) ([]OrphanWorktree, error) {
	if repoPath == "" || worktreeRoot == "" {
		return nil, nil
	}
	if _, err := os.Stat(worktreeRoot); os.IsNotExist(err) {
		return nil, nil
	}
	return collectOrphans(repoPath, worktreeRoot, keep)
}

// ReconcileWorktrees is the crash-recovery net: it removes cs orphan worktrees
// (and their cs-created branches) under worktreeRoot in repoPath, sparing any
// whose resolved path is in keep — the live sessions. Safe to run on every
// startup; idempotent.
func ReconcileWorktrees(repoPath, worktreeRoot, branchPrefix string, keep map[string]struct{}) error {
	return reconcile(repoPath, worktreeRoot, branchPrefix, keep, false /*nukeRoot*/, false /*forceBranchDelete*/)
}

// PruneWorktrees removes the explicitly named worktree paths from the repo at
// repoPath. Each path is classified against the cs roots (and branchPrefix); a
// path that looks foreign — a user-created worktree cs didn't make — is REFUSED
// and returned in skipped unless force is true. A branch is deleted only when it
// carries branchPrefix, so a borrowed pre-existing branch survives. When dryRun
// is set nothing is touched: removed/skipped report what would happen.
func PruneWorktrees(repoPath string, worktreePaths []string, roots []string, branchPrefix string, force, dryRun bool) (removed, skipped []string, err error) {
	if repoPath == "" || len(worktreePaths) == 0 {
		return nil, nil, nil
	}
	resolvedRoots := resolveRoots(roots)

	entries, listErr := listWorktreeEntries(repoPath)
	branchByPath := make(map[string]string, len(entries))
	for _, e := range entries {
		branchByPath[resolvePath(e.path)] = e.branch
	}

	var errs []error
	if listErr != nil {
		errs = append(errs, listErr)
	}
	didRemove := false
	for _, wp := range worktreePaths {
		resolved := resolvePath(wp)
		branch := branchByPath[resolved]
		if !force && !isCSManagedWorktree(resolved, branch, resolvedRoots, branchPrefix) {
			skipped = append(skipped, wp)
			continue
		}
		removed = append(removed, wp)
		if dryRun {
			continue
		}
		// Only delete the branch when it's clearly cs-created; otherwise leave it.
		delBranch := ""
		if branch != "" && branchPrefix != "" && strings.HasPrefix(branch, branchPrefix) {
			delBranch = branch
		}
		errs = append(errs, tearDownWorktree(repoPath, wp, delBranch)...)
		didRemove = true
	}

	if didRemove {
		if _, perr := exec.Command("git", "-C", repoPath, "worktree", "prune").CombinedOutput(); perr != nil {
			errs = append(errs, fmt.Errorf("prune worktrees in %s: %w", repoPath, perr))
		}
	}

	return removed, skipped, joinErrs("prune worktrees", errs)
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
