package ui

import (
	"claude-squad/log"
	"claude-squad/session"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// List.Kill logs via log.ErrorLog on the per-item RepoName failure path
	// (RepoName errors for not-yet-started instances). Without initializing
	// the package-level loggers, that nil-deref crashes the tests rather
	// than the code under test.
	log.Initialize(false)
	code := m.Run()
	log.Close()
	os.Exit(code)
}

// newTestInstance returns a session.Instance suitable for List-level tests.
// The instance is not started, so List.Kill() will skip the tmux/git cleanup
// paths and only exercise the selectedIdx / repos bookkeeping we care about.
func newTestInstance(t *testing.T, title, workspaceID string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:       title,
		Path:        ".",
		Program:     "true",
		WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("NewInstance(%q): %v", title, err)
	}
	return inst
}

// TestList_KillLastItem_KeepsSelectedIdxInBounds reproduces the panic that
// killed cs-edge mid-flow: after Start(true) failed, the TUI called
// list.Kill() to drop the broken instance, then instanceChanged() →
// GetSelectedInstance() → items[selectedIdx] indexed past the end.
//
// Root cause was Kill() relying on a deferred Up() to step selectedIdx back;
// Up() short-circuited when no visible item existed below, leaving the index
// stale. The fix is to clamp explicitly in Kill().
func TestList_KillLastItem_KeepsSelectedIdxInBounds(t *testing.T) {
	l := NewList(nil, false)
	for i := 0; i < 4; i++ {
		l.AddInstance(newTestInstance(t, fmt.Sprintf("inst-%d", i), ""))
	}
	l.selectedIdx = 3 // last

	l.Kill() // must not leave selectedIdx pointing past len(items)

	if l.selectedIdx >= len(l.items) {
		t.Fatalf("after Kill of last item: selectedIdx=%d, len=%d (must be in range)", l.selectedIdx, len(l.items))
	}
	if got := l.GetSelectedInstance(); got == nil {
		t.Fatalf("GetSelectedInstance returned nil after Kill of last (clamp should have stepped to the new last)")
	}
}

// TestList_KillLast_AllOthersFiltered exercises the exact path that crashed
// pre-fix: kill the last item while everything else is hidden by the view
// filter, so the old Up() helper found no visible candidate and bailed
// without touching selectedIdx. Post-fix, the explicit clamp in Kill() still
// drops selectedIdx into range even though no item is "visible".
func TestList_KillLast_AllOthersFiltered(t *testing.T) {
	l := NewList(nil, false)
	for i := 0; i < 3; i++ {
		l.AddInstance(newTestInstance(t, fmt.Sprintf("ws-a-%d", i), "ws-a"))
	}
	// 4th item is in a workspace we'll filter out so it's the only visible
	// one; killing it leaves no visible items below.
	l.AddInstance(newTestInstance(t, "ws-b-0", "ws-b"))
	l.viewFilter = "ws-b"
	l.selectedIdx = 3

	l.Kill()

	if l.selectedIdx >= len(l.items) || l.selectedIdx < 0 {
		t.Fatalf("selectedIdx out of bounds after Kill: %d (len=%d)", l.selectedIdx, len(l.items))
	}
	// GetSelectedInstance must not panic regardless of visibility.
	_ = l.GetSelectedInstance()
}

// TestList_GetSelectedInstance_OutOfBoundsReturnsNil is the defensive guard:
// even if some future mutation leaves selectedIdx stale, fetching the
// selection must return nil rather than panicking.
func TestList_GetSelectedInstance_OutOfBoundsReturnsNil(t *testing.T) {
	l := NewList(nil, false)
	l.AddInstance(newTestInstance(t, "only", ""))

	l.selectedIdx = 5 // forcibly stale
	if got := l.GetSelectedInstance(); got != nil {
		t.Errorf("expected nil for stale selectedIdx; got %v", got)
	}

	l.selectedIdx = -1 // also defensively
	if got := l.GetSelectedInstance(); got != nil {
		t.Errorf("expected nil for negative selectedIdx; got %v", got)
	}
}

// TestList_GetSelectedInstance_EmptyList is the existing contract: empty list
// returns nil. Kept as a regression guard around the new bounds checks.
func TestList_GetSelectedInstance_EmptyList(t *testing.T) {
	l := NewList(nil, false)
	if got := l.GetSelectedInstance(); got != nil {
		t.Errorf("empty list should return nil; got %v", got)
	}
}
