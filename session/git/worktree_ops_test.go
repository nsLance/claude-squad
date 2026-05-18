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
