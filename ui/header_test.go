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
	h.Update("1.0.18", "backend", 3, 2, "sessions")

	out := h.String()
	require.Contains(t, out, "claude-squad v1.0.18")
	require.Contains(t, out, "ns:")
	require.Contains(t, out, "backend")
	require.Contains(t, out, "sessions:")
	require.Contains(t, out, "workspaces:")
	// Hotkey hints present when there's room.
	require.Contains(t, out, "cmd")
	require.Contains(t, out, "quit")

	// Exactly the fixed number of rows.
	require.Equal(t, headerHeight, len(strings.Split(out, "\n")))
}

func TestHeader_Breadcrumb(t *testing.T) {
	h := NewHeader()
	h.SetSize(120)
	h.Update("1.0.18", "backend", 1, 1, "workspaces/sessions(backend)")

	lines := strings.Split(h.String(), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[1], "workspaces")
	require.Contains(t, lines[1], "sessions(backend)")
	require.Contains(t, lines[1], "›", "multi-segment breadcrumb should be joined with a separator")
}

func TestHeader_NarrowDropsHints(t *testing.T) {
	h := NewHeader()
	h.SetSize(30) // too narrow for context block + hints
	h.Update("1.0.18", "backend", 3, 2, "sessions")

	lines := strings.Split(h.String(), "\n")
	require.LessOrEqual(t, lipgloss.Width(lines[0]), 30, "banner must not overflow width")
	// Context block (logo) is kept even when hints are dropped.
	require.Contains(t, lines[0], "claude-squad")
}

func TestHeader_EmptyWorkspacePlaceholder(t *testing.T) {
	h := NewHeader()
	h.SetSize(120)
	h.Update("1.0.18", "", 0, 0, "")
	require.Contains(t, h.String(), "ns:")
}
