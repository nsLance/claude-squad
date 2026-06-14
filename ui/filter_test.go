package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

func TestList_TextFilter(t *testing.T) {
	l := NewList(nil, false)
	l.AddInstance(newTestInstance(t, "auth-refactor", "ws-a"))
	l.AddInstance(newTestInstance(t, "flaky-fix", "ws-a"))
	l.AddInstance(newTestInstance(t, "docs-pass", "ws-a"))
	l.SetSize(120, 20)

	l.SetTextFilter("fix")
	rows, _ := l.visibleRows()
	require.Len(t, rows, 1, "only flaky-fix matches 'fix'")

	out := l.RenderTableBody(120, 20)
	require.Contains(t, out, "flaky-fix")
	require.NotContains(t, out, "auth-refactor")

	// Filter is case-insensitive and also matches the branch.
	l.SetTextFilter("AUTH")
	rows, _ = l.visibleRows()
	require.Len(t, rows, 1)

	l.SetTextFilter("")
	rows, _ = l.visibleRows()
	require.Len(t, rows, 3)
}

func TestList_TextFilterKeepsNavigationInsideMatches(t *testing.T) {
	l := NewList(nil, false)
	l.AddInstance(newTestInstance(t, "alpha", "ws"))
	l.AddInstance(newTestInstance(t, "beta", "ws"))
	l.AddInstance(newTestInstance(t, "alpine", "ws"))
	l.selectedIdx = 1 // beta

	l.SetTextFilter("alp") // matches alpha (0) and alpine (2), hides beta
	// Selection should have snapped to a visible item.
	require.True(t, l.isItemVisible(l.GetSelectedInstance()))

	// Down from a match lands on the next match, skipping the hidden 'beta'.
	l.SetSelectedInstance(0)
	l.Down()
	require.Equal(t, "alpine", l.GetSelectedInstance().Title)
}

func TestTable_Filter(t *testing.T) {
	tbl := NewTable([]Column{
		{Title: "NAME", MinWidth: 6, Weight: 1, Align: lipgloss.Left,
			Render: func(r any) string { return r.(string) }},
	})
	tbl.SetSize(40, 10)
	tbl.SetRows([]any{"sophon", "internal-atlas", "claude-squad"})

	tbl.SetFilter("at")
	require.Equal(t, 1, tbl.Len(), "only internal-atlas contains 'at'")
	out := tbl.String()
	require.Contains(t, out, "internal-atlas")
	require.NotContains(t, out, "sophon")

	tbl.SetFilter("")
	require.Equal(t, 3, tbl.Len())
}

func TestTable_FilterStripsANSI(t *testing.T) {
	colored := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("running")
	tbl := NewTable([]Column{
		{Title: "S", MinWidth: 8, Weight: 1, Align: lipgloss.Left,
			Render: func(r any) string { return colored }},
	})
	tbl.SetSize(40, 10)
	tbl.SetRows([]any{"x"})
	tbl.SetFilter("running") // must match despite ANSI styling
	require.Equal(t, 1, tbl.Len())
	require.True(t, strings.Contains(stripANSI(colored), "running"))
}
