package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

func TestHeader_BannerContent(t *testing.T) {
	h := NewHeader()
	h.SetSize(120)
	h.Update("1.0.18", "backend", 3, 2, "sessions", "")
	h.SetShortcuts([]MenuEntry{{Key: "q", Desc: "quit"}})

	out := h.String()
	require.Contains(t, out, "claude-squad v1.0.18")
	require.Contains(t, out, "ns:")
	require.Contains(t, out, "backend")
	require.Contains(t, out, "sessions:")
	require.Contains(t, out, "workspaces:")
	// Navigation primitives and the contextual shortcuts render in the box.
	require.Contains(t, out, "command")
	require.Contains(t, out, "filter")
	require.Contains(t, out, "quit")

	// Exactly the fixed number of rows.
	require.Equal(t, headerHeight, len(strings.Split(out, "\n")))
}

func TestHeader_Breadcrumb(t *testing.T) {
	h := NewHeader()
	h.SetSize(120)
	h.Update("1.0.18", "backend", 1, 1, "workspaces/sessions(backend)", "")

	lines := strings.Split(h.String(), "\n")
	require.Len(t, lines, headerHeight)
	// The breadcrumb is the last row, beneath the box.
	crumb := lines[len(lines)-1]
	require.Contains(t, crumb, "workspaces")
	require.Contains(t, crumb, "sessions(backend)")
	require.Contains(t, crumb, "›", "multi-segment breadcrumb should be joined with a separator")
}

func TestHeader_NarrowFallback(t *testing.T) {
	h := NewHeader()
	h.SetSize(30) // too narrow for the box
	h.Update("1.0.18", "backend", 3, 2, "sessions", "")

	lines := strings.Split(h.String(), "\n")
	for i, l := range lines {
		require.LessOrEqualf(t, lipgloss.Width(l), 30, "row %d must not overflow width", i)
	}
	require.Contains(t, lines[0], "claude-squad")
}

func TestHeader_EmptyWorkspacePlaceholder(t *testing.T) {
	h := NewHeader()
	h.SetSize(120)
	h.Update("1.0.18", "", 0, 0, "", "")
	require.Contains(t, h.String(), "ns:")
}
