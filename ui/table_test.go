package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// twoColTable builds a simple sortable string/number table for tests.
func twoColTable() *Table {
	return NewTable([]Column{
		{
			Title: "NAME", MinWidth: 6, Weight: 1, Align: lipgloss.Left,
			Render: func(r any) string { return r.([2]string)[0] },
			Less:   func(a, b any) bool { return a.([2]string)[0] < b.([2]string)[0] },
		},
		{
			Title: "VAL", MinWidth: 4, Weight: 0, Align: lipgloss.Right,
			Render: func(r any) string { return r.([2]string)[1] },
			Less:   func(a, b any) bool { return a.([2]string)[1] < b.([2]string)[1] },
		},
	})
}

func TestTable_HeaderAndRows(t *testing.T) {
	tbl := twoColTable()
	tbl.SetSize(40, 10)
	tbl.SetRows([]any{[2]string{"alpha", "3"}, [2]string{"beta", "1"}})

	out := tbl.String()
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 3, "header + 2 rows")
	require.Contains(t, lines[0], "NAME")
	require.Contains(t, lines[0], "VAL")
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "beta")
}

func TestTable_SelectionMarker(t *testing.T) {
	tbl := twoColTable()
	tbl.SetSize(40, 10)
	tbl.SetRows([]any{[2]string{"alpha", "3"}, [2]string{"beta", "1"}})
	tbl.SetSelected(1)

	out := tbl.String()
	require.Contains(t, out, "▸", "selected row should carry the cursor marker")
	// The marker must sit on the selected (beta) row, not the first row.
	lines := strings.Split(out, "\n")
	var betaLine string
	for _, l := range lines {
		if strings.Contains(l, "beta") {
			betaLine = l
		}
	}
	require.Contains(t, betaLine, "▸")
}

func TestTable_Truncation(t *testing.T) {
	tbl := twoColTable()
	tbl.SetSize(16, 10) // narrow: NAME column will be tight
	tbl.SetRows([]any{[2]string{"a-very-long-name-indeed", "9"}})

	out := tbl.String()
	require.Contains(t, out, "…", "an over-long plain cell should be truncated with an ellipsis")
	// No rendered line may exceed the table width.
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 16, "line wider than table: %q", l)
	}
}

func TestTable_NarrowDropsColumns(t *testing.T) {
	tbl := twoColTable()
	// Only wide enough for the marker + NAME(6); VAL(4)+sep won't fit.
	tbl.SetSize(markerWidth+6, 10)
	tbl.SetRows([]any{[2]string{"alpha", "3"}})

	_, included := tbl.computeWidths()
	require.Equal(t, []int{0}, included, "narrow pane should drop the VAL column")
}

func TestTable_Sort(t *testing.T) {
	tbl := twoColTable()
	tbl.SetSize(40, 10)
	tbl.SetRows([]any{[2]string{"beta", "1"}, [2]string{"alpha", "3"}})

	// Natural order preserved until a sort is requested.
	require.Equal(t, "beta", tbl.rows[0].([2]string)[0])

	tbl.CycleSort() // → sort by NAME asc
	require.Equal(t, "alpha", tbl.rows[0].([2]string)[0])
	require.Equal(t, "beta", tbl.rows[1].([2]string)[0])

	tbl.CycleSort() // → NAME desc (revisits same column)
	require.Equal(t, "beta", tbl.rows[0].([2]string)[0])
}

func TestTable_MoveSelection(t *testing.T) {
	tbl := twoColTable()
	tbl.SetRows([]any{[2]string{"a", "1"}, [2]string{"b", "2"}, [2]string{"c", "3"}})
	require.Equal(t, 0, tbl.Selected())
	tbl.MoveUp() // clamp at 0
	require.Equal(t, 0, tbl.Selected())
	tbl.MoveDown()
	tbl.MoveDown()
	require.Equal(t, 2, tbl.Selected())
	tbl.MoveDown() // clamp at last
	require.Equal(t, 2, tbl.Selected())
}
