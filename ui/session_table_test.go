package ui

import (
	"testing"
	"time"

	"claude-squad/session"

	"github.com/stretchr/testify/require"
)

func TestRenderTable_HeaderAndScoping(t *testing.T) {
	l := NewList(nil, false)
	l.AddInstance(newTestInstance(t, "auth-refactor", "ws-a"))
	l.AddInstance(newTestInstance(t, "flaky-fix", "ws-a"))
	l.AddInstance(newTestInstance(t, "docs-pass", "ws-b"))
	l.SetSize(120, 20)

	// Unscoped: column header present, all three sessions shown, WORKSPACE column visible.
	out := l.RenderTable()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "STATUS")
	require.Contains(t, out, "WORKSPACE")
	for _, name := range []string{"auth-refactor", "flaky-fix", "docs-pass"} {
		require.Contains(t, out, name)
	}

	// Scoped to ws-a: docs-pass (ws-b) is filtered out; WORKSPACE column dropped.
	l.SetViewFilter("ws-a")
	scoped := l.RenderTable()
	require.Contains(t, scoped, "auth-refactor")
	require.NotContains(t, scoped, "docs-pass")
	require.NotContains(t, scoped, "WORKSPACE")
}

func TestVisibleRows_SelectionIndex(t *testing.T) {
	l := NewList(nil, false)
	l.AddInstance(newTestInstance(t, "a", "ws-a"))
	l.AddInstance(newTestInstance(t, "b", "ws-b"))
	l.AddInstance(newTestInstance(t, "c", "ws-a"))
	l.selectedIdx = 2 // "c"

	l.SetViewFilter("ws-a") // visible: a, c
	rows, selIdx := l.visibleRows()
	require.Len(t, rows, 2)
	require.Equal(t, 1, selIdx, "selected 'c' is the 2nd visible row")

	// Selecting a now-hidden item yields selIdx -1.
	l.selectedIdx = 1 // "b" (ws-b, hidden)
	_, selIdx = l.visibleRows()
	require.Equal(t, -1, selIdx)
}

func TestRenderTable_EmptyState(t *testing.T) {
	l := NewList(nil, false)
	l.SetActiveWorkspace("backend")
	l.SetSize(120, 20)
	out := l.RenderTable()
	require.Contains(t, out, "No sessions")
}

func TestHumanizeAge(t *testing.T) {
	now := time.Now()
	require.Equal(t, "30s", humanizeAge(now.Add(-30*time.Second)))
	require.Equal(t, "5m", humanizeAge(now.Add(-5*time.Minute)))
	require.Equal(t, "3h", humanizeAge(now.Add(-3*time.Hour)))
	require.Equal(t, "2d", humanizeAge(now.Add(-48*time.Hour)))
}

func TestStatusRank_Ordering(t *testing.T) {
	// running sorts before paused before exited.
	require.Less(t, statusRank(session.Running), statusRank(session.Paused))
	require.Less(t, statusRank(session.Paused), statusRank(session.Exited))
}
