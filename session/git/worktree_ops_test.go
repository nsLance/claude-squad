package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyWorktreeAddErr_BranchCollision(t *testing.T) {
	raw := errors.New("git command failed: Preparing worktree (checking out 'nakkul/cs-edge')\nfatal: 'nakkul/cs-edge' is already used by worktree at '/Users/nakkul/.claude-squad/workspaces/c17715fb8a23/worktrees/nakkul/cs-edge_18aeae1d36670f48'\n (exit status 128)")

	err := classifyWorktreeAddErr("nakkul/cs-edge", raw)

	var bce *BranchCollisionError
	if !errors.As(err, &bce) {
		t.Fatalf("expected BranchCollisionError, got %T: %v", err, err)
	}
	if bce.Branch != "nakkul/cs-edge" {
		t.Errorf("Branch = %q, want %q", bce.Branch, "nakkul/cs-edge")
	}
	want := "/Users/nakkul/.claude-squad/workspaces/c17715fb8a23/worktrees/nakkul/cs-edge_18aeae1d36670f48"
	if bce.WorktreePath != want {
		t.Errorf("WorktreePath = %q, want %q", bce.WorktreePath, want)
	}
	// Message should be self-contained enough that an errBox showing it tells
	// the user both what's wrong and how to fix it.
	msg := bce.Error()
	for _, want := range []string{"nakkul/cs-edge", "/Users/nakkul/.claude-squad/workspaces/c17715fb8a23/worktrees/nakkul/cs-edge_18aeae1d36670f48", "git worktree remove"} {
		if !contains(msg, want) {
			t.Errorf("error message missing %q; got: %s", want, msg)
		}
	}
}

func TestClassifyWorktreeAddErr_PassthroughUnrelated(t *testing.T) {
	raw := fmt.Errorf("git command failed: fatal: not a git repository (exit status 128)")
	err := classifyWorktreeAddErr("foo", raw)
	var bce *BranchCollisionError
	if errors.As(err, &bce) {
		t.Fatalf("unrelated error should pass through unchanged, got BranchCollisionError")
	}
	if err.Error() != raw.Error() {
		t.Errorf("unrelated error rewritten: got %q, want %q", err.Error(), raw.Error())
	}
}

func TestClassifyWorktreeAddErr_NilPassthrough(t *testing.T) {
	if classifyWorktreeAddErr("foo", nil) != nil {
		t.Error("nil input should yield nil output")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// initTestRepo builds a tiny repo with one empty commit so `git worktree add`
// works against it. Returns the repo path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// TestCleanupWorkspaceWorktrees verifies the per-workspace cleanup path used
// by `cs reset`: every worktree whose path lives under worktreeRoot must be
// torn down (worktree removed, branch deleted, registry pruned), while
// unrelated worktrees on the same repo are untouched. This is the path that
// was missing pre-fix — CleanupWorktrees only swept the legacy global dir, so
// per-workspace orphans survived reset and went on to block future sessions.
func TestCleanupWorkspaceWorktrees(t *testing.T) {
	repo := initTestRepo(t)
	root := filepath.Join(t.TempDir(), "wsroot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two worktrees under root (should be cleaned) + one outside (should
	// survive — proves the prefix filter works).
	under1 := filepath.Join(root, "under1")
	under2 := filepath.Join(root, "nested", "under2")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, p := range []struct{ path, branch string }{
		{under1, "feat/a"}, {under2, "feat/b"}, {outside, "feat/c"},
	} {
		out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", p.branch, p.path).CombinedOutput()
		if err != nil {
			t.Fatalf("worktree add %s: %v: %s", p.path, err, out)
		}
	}

	if err := CleanupWorkspaceWorktrees(repo, root); err != nil {
		t.Fatalf("CleanupWorkspaceWorktrees: %v", err)
	}

	// under1, under2 and the root itself should be gone.
	for _, p := range []string{under1, under2, root} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, got stat err=%v", p, err)
		}
	}
	// outside survives.
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("outside worktree should survive cleanup: %v", err)
	}

	// Branches inside root are deleted; the outside one remains.
	out, err := exec.Command("git", "-C", repo, "branch", "--list").Output()
	if err != nil {
		t.Fatal(err)
	}
	branches := string(out)
	for _, b := range []string{"feat/a", "feat/b"} {
		if strings.Contains(branches, b) {
			t.Errorf("branch %s should have been deleted; branches:\n%s", b, branches)
		}
	}
	if !strings.Contains(branches, "feat/c") {
		t.Errorf("branch feat/c (outside the workspace root) should survive; branches:\n%s", branches)
	}
}

// TestCleanupWorkspaceWorktrees_MissingRootIsNoop is the idempotency guarantee
// `cs reset` relies on: registries may list workspaces whose on-disk root has
// already been swept, and that must not fail the reset.
func TestCleanupWorkspaceWorktrees_MissingRootIsNoop(t *testing.T) {
	repo := initTestRepo(t)
	if err := CleanupWorkspaceWorktrees(repo, "/nonexistent/path/should/not/exist"); err != nil {
		t.Errorf("missing root should be a noop, got: %v", err)
	}
}

// TestRemoveOrphanWorktree mirrors the overlay-cleanup path: confirm-yes runs
// this against a single worktree path and expects it to vanish along with the
// branch. This is the surgical-single-target counterpart to the bulk cleanup.
func TestRemoveOrphanWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "orphan")
	out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", "nakkul/orphan", wt).CombinedOutput()
	if err != nil {
		t.Fatalf("worktree add: %v: %s", err, out)
	}

	if err := RemoveOrphanWorktree(repo, wt, "nakkul/orphan"); err != nil {
		t.Fatalf("RemoveOrphanWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone: stat err=%v", err)
	}
	branches, _ := exec.Command("git", "-C", repo, "branch", "--list").Output()
	if strings.Contains(string(branches), "nakkul/orphan") {
		t.Errorf("branch should be gone: %s", branches)
	}
}

func TestSetupFromExistingBranch_RemovesOrphanedDirectory(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	worktreePath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir orphaned worktree: %v", err)
	}

	junkPath := filepath.Join(worktreePath, "orphan.txt")
	if err := os.WriteFile(junkPath, []byte("orphaned\n"), 0644); err != nil {
		t.Fatalf("write orphan marker: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if _, err := os.Stat(junkPath); !os.IsNotExist(err) {
		t.Fatalf("orphan marker still exists after Setup, err = %v", err)
	}

	if valid, err := g.IsValidWorktree(); err != nil {
		t.Fatalf("IsValidWorktree() error = %v", err)
	} else if !valid {
		t.Fatal("expected Setup() to recreate a valid worktree")
	}

	currentBranch := mustRunGit(t, worktreePath, "branch", "--show-current")
	if currentBranch != "feature/test\n" {
		t.Fatalf("current branch = %q, want %q", currentBranch, "feature/test\n")
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}

	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

// --- worktree reconciler / prune tests ---

func addWorktree(t *testing.T, repo, path, branch string) {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", branch, path).CombinedOutput()
	if err != nil {
		t.Fatalf("worktree add %s (%s): %v: %s", path, branch, err, out)
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run()
	return err == nil
}

func gone(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func mkRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "wsroot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestIsCSManagedWorktree(t *testing.T) {
	root := t.TempDir()
	roots := resolveRoots([]string{root})
	underRoot := filepath.Join(root, "nakkul", "a_123")
	if err := os.MkdirAll(underRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "b_456")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		path, branch string
		prefix       string
		want         bool
	}{
		{"under-root prefixed", underRoot, "nakkul/a", "nakkul/", true},
		{"under-root non-prefixed", underRoot, "feature-x", "nakkul/", true},
		{"outside prefixed", outside, "nakkul/b", "nakkul/", true},
		{"outside non-prefixed", outside, "feature-y", "nakkul/", false},
		{"empty prefix disables branch signal", outside, "nakkul/b", "", false},
	}
	for _, c := range cases {
		if got := isCSManagedWorktree(c.path, c.branch, roots, c.prefix); got != c.want {
			t.Errorf("%s: isCSManagedWorktree = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReconcileWorktrees_RemovesOrphanKeepsLive(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	a := filepath.Join(root, "nakkul", "a_1")
	b := filepath.Join(root, "nakkul", "b_2")
	c := filepath.Join(root, "nakkul", "c_3")
	addWorktree(t, repo, a, "nakkul/a")
	addWorktree(t, repo, b, "nakkul/b")
	addWorktree(t, repo, c, "nakkul/c")

	keep := map[string]struct{}{resolvePath(c): {}}
	if err := ReconcileWorktrees(repo, root, "nakkul/", keep); err != nil {
		t.Fatalf("ReconcileWorktrees: %v", err)
	}

	if !gone(a) || !gone(b) {
		t.Errorf("orphan worktrees should be removed: a gone=%v b gone=%v", gone(a), gone(b))
	}
	if gone(c) {
		t.Errorf("live worktree c should survive")
	}
	if gone(root) {
		t.Errorf("root must NOT be nuked by reconcile (only by reset)")
	}
	if branchExists(t, repo, "nakkul/a") || branchExists(t, repo, "nakkul/b") {
		t.Errorf("orphan branches should be deleted")
	}
	if !branchExists(t, repo, "nakkul/c") {
		t.Errorf("live branch nakkul/c should survive")
	}

	// Idempotent: a second run is a clean noop.
	if err := ReconcileWorktrees(repo, root, "nakkul/", keep); err != nil {
		t.Errorf("second reconcile should be noop, got: %v", err)
	}
}

func TestReconcileWorktrees_ForeignRootUntouched(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	under := filepath.Join(root, "nakkul", "a_1")
	outside := filepath.Join(t.TempDir(), "outside")
	addWorktree(t, repo, under, "nakkul/a")
	addWorktree(t, repo, outside, "nakkul/out")

	if err := ReconcileWorktrees(repo, root, "nakkul/", nil); err != nil {
		t.Fatalf("ReconcileWorktrees: %v", err)
	}
	if !gone(under) {
		t.Errorf("worktree under root should be removed")
	}
	if gone(outside) {
		t.Errorf("worktree outside root must never be touched")
	}
	if !branchExists(t, repo, "nakkul/out") {
		t.Errorf("outside branch should survive")
	}
}

func TestReconcileWorktrees_PreservesNonPrefixBranch(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	wt := filepath.Join(root, "borrowed_1")
	addWorktree(t, repo, wt, "feature-x") // under root (cs-managed) but not a cs branch

	if err := ReconcileWorktrees(repo, root, "nakkul/", nil); err != nil {
		t.Fatalf("ReconcileWorktrees: %v", err)
	}
	if !gone(wt) {
		t.Errorf("worktree under root should be removed")
	}
	if !branchExists(t, repo, "feature-x") {
		t.Errorf("non-cs branch feature-x must survive (only the worktree goes)")
	}
}

func TestReconcileWorktrees_DanglingDirSwept(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	live := filepath.Join(root, "nakkul", "live_1")
	addWorktree(t, repo, live, "nakkul/live")

	// A dangling worktree dir git no longer tracks: a directory carrying a .git
	// gitlink but with no registry entry.
	dangling := filepath.Join(root, "nakkul", "dangling_9")
	if err := os.MkdirAll(dangling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dangling, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	keep := map[string]struct{}{resolvePath(live): {}}
	if err := ReconcileWorktrees(repo, root, "nakkul/", keep); err != nil {
		t.Fatalf("ReconcileWorktrees: %v", err)
	}
	if !gone(dangling) {
		t.Errorf("dangling dir should be swept")
	}
	if gone(live) {
		t.Errorf("live worktree should survive the sweep")
	}
}

func TestPruneWorktrees_SkipsForeignUnlessForced(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	csWt := filepath.Join(root, "nakkul", "cs_1") // under root => cs-managed
	foreign := filepath.Join(t.TempDir(), "foreign")
	addWorktree(t, repo, csWt, "nakkul/cs")
	addWorktree(t, repo, foreign, "feature-z")
	roots := []string{root}

	removed, skipped, err := PruneWorktrees(repo, []string{csWt, foreign}, roots, "nakkul/", false /*force*/, false /*dryRun*/)
	if err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if !gone(csWt) {
		t.Errorf("cs-managed worktree should be pruned")
	}
	if gone(foreign) {
		t.Errorf("foreign worktree must be refused without --force")
	}
	if len(removed) != 1 || removed[0] != csWt {
		t.Errorf("removed = %v, want [%s]", removed, csWt)
	}
	if len(skipped) != 1 || skipped[0] != foreign {
		t.Errorf("skipped = %v, want [%s]", skipped, foreign)
	}

	// With --force the foreign worktree goes too.
	if _, _, err := PruneWorktrees(repo, []string{foreign}, roots, "nakkul/", true /*force*/, false); err != nil {
		t.Fatalf("PruneWorktrees force: %v", err)
	}
	if !gone(foreign) {
		t.Errorf("foreign worktree should be removed with --force")
	}
}

func TestPruneWorktrees_DryRunTouchesNothing(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	csWt := filepath.Join(root, "nakkul", "cs_1")
	addWorktree(t, repo, csWt, "nakkul/cs")

	removed, _, err := PruneWorktrees(repo, []string{csWt}, []string{root}, "nakkul/", false, true /*dryRun*/)
	if err != nil {
		t.Fatalf("PruneWorktrees dry-run: %v", err)
	}
	if len(removed) != 1 {
		t.Errorf("dry-run should report 1 removable, got %v", removed)
	}
	if gone(csWt) {
		t.Errorf("dry-run must not actually remove anything")
	}
}

func TestPruneWorktrees_DeletesPrefixedBranchOnly(t *testing.T) {
	repo := initTestRepo(t)
	root := mkRoot(t)
	prefixed := filepath.Join(root, "nakkul", "p_1")
	borrowed := filepath.Join(root, "borrowed_1")
	addWorktree(t, repo, prefixed, "nakkul/p")
	addWorktree(t, repo, borrowed, "feature-b") // under root, cs-managed by path, branch not ours

	if _, _, err := PruneWorktrees(repo, []string{prefixed, borrowed}, []string{root}, "nakkul/", false, false); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if !gone(prefixed) || !gone(borrowed) {
		t.Errorf("both worktree dirs should be removed")
	}
	if branchExists(t, repo, "nakkul/p") {
		t.Errorf("cs-prefixed branch should be deleted")
	}
	if !branchExists(t, repo, "feature-b") {
		t.Errorf("non-cs branch feature-b must survive")
	}
}
