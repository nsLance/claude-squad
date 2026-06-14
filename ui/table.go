package ui

import (
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ansiRe strips SGR escape sequences so filter matching sees plain cell text.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// markerWidth is the fixed left gutter the table reserves for the selection
// marker ("▸ " or two spaces). Using a gutter marker instead of a full-row
// background keeps every cell's own colors intact (a background wrapping
// ANSI-colored cells breaks at the inner reset codes).
const markerWidth = 2

var tableHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var selectedMarkerStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var selectedNameStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#ffffff"})

// Column describes one column of a Table. Render returns the (possibly
// ANSI-styled) content for a row; the table handles padding, alignment, and
// truncation. Weight distributes leftover width after MinWidths are satisfied.
// Less, when non-nil, makes the column sortable.
type Column struct {
	Title    string
	MinWidth int
	Weight   int
	Align    lipgloss.Position
	Render   func(row any) string
	Less     func(a, b any) bool
}

// Table is a reusable columnar renderer with a selection cursor, optional
// sorting, and graceful column-dropping when the pane is too narrow to fit
// every column. It is a pure renderer: callers set rows/size/selection and call
// String(). Navigation helpers (MoveUp/MoveDown) are provided for views that
// own a Table directly.
type Table struct {
	columns     []Column
	allRows     []any // full set as supplied
	rows        []any // filtered + sorted, what renders
	filter      string
	selectedIdx int
	width       int
	height      int
	sortKey     int // index into columns; -1 = natural order
	sortAsc     bool
}

func NewTable(cols []Column) *Table {
	return &Table{columns: cols, sortKey: -1, sortAsc: true}
}

// SetRows replaces the full row set and re-applies the current filter + sort.
func (t *Table) SetRows(rows []any) {
	t.allRows = rows
	t.rebuild()
}

// SetFilter sets a case-insensitive substring filter applied across all cells.
// Empty clears it.
func (t *Table) SetFilter(f string) {
	t.filter = f
	t.rebuild()
}

// rebuild recomputes the visible rows from allRows by filtering then sorting,
// preserving the selected row by identity and clamping the index.
func (t *Table) rebuild() {
	sel := t.SelectedRow()
	t.rows = t.rows[:0]
	for _, r := range t.allRows {
		if t.rowMatches(r) {
			t.rows = append(t.rows, r)
		}
	}
	t.sortRows()
	t.selectedIdx = 0
	if sel != nil {
		for i, r := range t.rows {
			if r == sel {
				t.selectedIdx = i
				break
			}
		}
	}
	if t.selectedIdx >= len(t.rows) {
		t.selectedIdx = len(t.rows) - 1
	}
	if t.selectedIdx < 0 {
		t.selectedIdx = 0
	}
}

// rowMatches reports whether a row passes the current filter (any cell, ANSI
// stripped, case-insensitive substring).
func (t *Table) rowMatches(row any) bool {
	if t.filter == "" {
		return true
	}
	needle := strings.ToLower(t.filter)
	for _, c := range t.columns {
		if strings.Contains(strings.ToLower(stripANSI(c.Render(row))), needle) {
			return true
		}
	}
	return false
}

func (t *Table) SetSize(w, h int) { t.width, t.height = w, h }
func (t *Table) SetSelected(i int) {
	if i >= 0 && i < len(t.rows) {
		t.selectedIdx = i
	}
}
func (t *Table) Selected() int { return t.selectedIdx }
func (t *Table) Len() int      { return len(t.rows) }

// SelectedRow returns the currently-selected row, or nil if empty.
func (t *Table) SelectedRow() any {
	if t.selectedIdx >= 0 && t.selectedIdx < len(t.rows) {
		return t.rows[t.selectedIdx]
	}
	return nil
}

func (t *Table) MoveUp() {
	if t.selectedIdx > 0 {
		t.selectedIdx--
	}
}

func (t *Table) MoveDown() {
	if t.selectedIdx < len(t.rows)-1 {
		t.selectedIdx++
	}
}

// CycleSort advances the sort to the next sortable column. Revisiting the
// current sort column flips its direction; moving to a new column starts
// ascending.
func (t *Table) CycleSort() {
	n := len(t.columns)
	for off := 0; off <= n; off++ {
		idx := ((t.sortKey + 1 + off) % n)
		if t.columns[idx].Less == nil {
			continue
		}
		if idx == t.sortKey {
			t.sortAsc = !t.sortAsc
		} else {
			t.sortKey, t.sortAsc = idx, true
		}
		t.rebuild()
		return
	}
}

// sortRows stable-sorts t.rows in place by the active sort column.
func (t *Table) sortRows() {
	if t.sortKey < 0 || t.sortKey >= len(t.columns) || t.columns[t.sortKey].Less == nil {
		return
	}
	less := t.columns[t.sortKey].Less
	sort.SliceStable(t.rows, func(a, b int) bool {
		if t.sortAsc {
			return less(t.rows[a], t.rows[b])
		}
		return less(t.rows[b], t.rows[a])
	})
}

// computeWidths greedily includes columns left-to-right until the pane is full
// (dropping the rest, k9s-style), then distributes leftover width by Weight.
func (t *Table) computeWidths() (widths []int, included []int) {
	usable := t.width - markerWidth
	widths = make([]int, len(t.columns))
	used := 0
	for i, c := range t.columns {
		need := c.MinWidth
		if len(included) > 0 {
			need++ // 1-space separator before this column
		}
		if used+need > usable {
			break
		}
		used += need
		widths[i] = c.MinWidth
		included = append(included, i)
	}
	leftover := usable - used
	if leftover > 0 {
		totalWeight := 0
		for _, i := range included {
			totalWeight += t.columns[i].Weight
		}
		if totalWeight > 0 {
			distributed := 0
			for _, i := range included {
				add := leftover * t.columns[i].Weight / totalWeight
				widths[i] += add
				distributed += add
			}
			// Hand any rounding remainder to the last included column.
			if rem := leftover - distributed; rem > 0 && len(included) > 0 {
				widths[included[len(included)-1]] += rem
			}
		}
	}
	return widths, included
}

func (t *Table) String() string {
	if t.width <= 0 || len(t.columns) == 0 {
		return ""
	}
	widths, included := t.computeWidths()
	if len(included) == 0 {
		return ""
	}

	lines := []string{t.renderHeader(widths, included)}

	rowLines := make([]string, len(t.rows))
	for i, r := range t.rows {
		rowLines[i] = t.renderRow(r, widths, included, i == t.selectedIdx)
	}

	avail := t.height - 1 // header consumes one line
	if avail < 1 {
		avail = 1
	}
	if len(rowLines) > avail {
		rowLines = scrollClamp(rowLines, t.selectedIdx, avail)
	}
	lines = append(lines, rowLines...)
	return strings.Join(lines, "\n")
}

func (t *Table) renderHeader(widths, included []int) string {
	parts := []string{strings.Repeat(" ", markerWidth)}
	for n, ci := range included {
		title := t.columns[ci].Title
		if ci == t.sortKey {
			if t.sortAsc {
				title += " ▲"
			} else {
				title += " ▼"
			}
		}
		if n > 0 {
			parts = append(parts, " ")
		}
		parts = append(parts, fitCell(title, widths[ci], t.columns[ci].Align))
	}
	return tableHeaderStyle.Render(strings.Join(parts, ""))
}

func (t *Table) renderRow(row any, widths, included []int, selected bool) string {
	marker := "  "
	if selected {
		marker = selectedMarkerStyle.Render("▸ ")
	}
	parts := []string{marker}
	for n, ci := range included {
		content := t.columns[ci].Render(row)
		// Bold the first (name) column on the selected row for emphasis.
		if selected && n == 0 {
			content = selectedNameStyle.Render(content)
		}
		if n > 0 {
			parts = append(parts, " ")
		}
		parts = append(parts, fitCell(content, widths[ci], t.columns[ci].Align))
	}
	return strings.Join(parts, "")
}

// fitCell truncates (only plain, ANSI-free content) and pads s to exactly width
// display columns, aligned per align. ANSI-styled cells are assumed short
// enough to fit (their columns are sized to their content) and are only padded.
func fitCell(s string, width int, align lipgloss.Position) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w > width {
		if !strings.ContainsRune(s, 0x1b) { // plain text → safe to truncate
			s = runewidth.Truncate(s, width, "…")
			w = lipgloss.Width(s)
		} else {
			return s // can't safely truncate styled content; leave as-is
		}
	}
	pad := width - w
	if pad <= 0 {
		return s
	}
	if align == lipgloss.Right {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}
