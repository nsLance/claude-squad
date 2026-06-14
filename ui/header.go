package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Header geometry. The header is a k9s/a9s-style band: a bordered shortcut box
// on the left (context line + a column-major grid of keybindings) and a
// stylized ASCII wordmark on the right, with a breadcrumb row beneath.
const (
	shortcutGridRows = 4
	// headerBoxHeight = rounded border (2) + context line (1) + grid rows (4).
	headerBoxHeight = 2 + 1 + shortcutGridRows
	// headerHeight adds the breadcrumb row beneath the box.
	headerHeight = headerBoxHeight + 1
)

// logoArt is the stylized "cs" wordmark shown in the top-right (a9s-style).
var logoArt = []string{
	" ██████╗ ███████╗",
	"██╔════╝ ██╔════╝",
	"██║      ███████╗",
	"██║      ╚════██║",
	"╚██████╗ ███████║",
}

var logoArtStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("99"))

var logoTagStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var headerBoxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("62")).
	Padding(0, 1)

var headerLogoStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var headerLabelStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var headerValueStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var shortcutBracketStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#9C9494", Dark: "#5C5C5C"})

var shortcutKeyStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var shortcutDescStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var shortcutActionStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("99"))

var breadcrumbStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

var breadcrumbSepStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

// filterIndicatorStyle highlights the active "/" filter on the breadcrumb row.
var filterIndicatorStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#1a1a1a")).
	Background(lipgloss.Color("#FFCC00"))

// Header renders the k9s-style top band (shortcut box + ASCII wordmark) and a
// breadcrumb line for the current view. It holds no application state of its
// own; callers refresh its fields each frame via Update/SetShortcuts.
type Header struct {
	width int

	version        string
	activeWS       string
	sessionCount   int
	workspaceCount int
	breadcrumb     string
	filter         string
	shortcuts      []MenuEntry
}

func NewHeader() *Header { return &Header{} }

func (h *Header) SetSize(w int) { h.width = w }

// Height returns the fixed row count the header occupies.
func (h *Header) Height() int { return headerHeight }

// Update refreshes the data shown in the header. filter is the active "/"-filter
// text (empty when none), shown as an indicator on the breadcrumb row.
func (h *Header) Update(version, activeWS string, sessions, workspaces int, breadcrumb, filter string) {
	h.version = version
	h.activeWS = activeWS
	h.sessionCount = sessions
	h.workspaceCount = workspaces
	h.breadcrumb = breadcrumb
	h.filter = filter
}

// SetShortcuts records the contextual keybindings (from the menu) to render in
// the shortcut box. The navigation primitives (":" / "/") are prepended.
func (h *Header) SetShortcuts(entries []MenuEntry) { h.shortcuts = entries }

func (h *Header) String() string {
	if h.width <= 0 {
		return strings.Repeat("\n", headerHeight-1)
	}
	return lipgloss.JoinVertical(lipgloss.Left, h.topBand(), h.breadcrumbRow())
}

// topBand joins the shortcut box (left) with the ASCII wordmark (right).
func (h *Header) topBand() string {
	box := h.shortcutBox()
	boxW := lipgloss.Width(box)
	if boxW > h.width {
		// Terminal too narrow for the box — fall back to a clamped one-liner.
		return h.compactBand()
	}

	logo := h.logoBlock()
	logoW := lipgloss.Width(logo)

	gap := h.width - boxW - logoW
	if gap < 2 || logoW == 0 {
		// Not enough room for the wordmark — just show the box.
		return box
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, box, strings.Repeat(" ", gap), logo)
}

// compactBand is the narrow-terminal fallback: a single plain context line,
// hard-truncated to width, padded to the band height.
func (h *Header) compactBand() string {
	ws := h.activeWS
	if ws == "" {
		ws = "—"
	}
	line := fmt.Sprintf("claude-squad v%s  ns:%s  sessions:%d  workspaces:%d",
		h.version, ws, h.sessionCount, h.workspaceCount)
	return lipgloss.Place(h.width, headerBoxHeight, lipgloss.Left, lipgloss.Top,
		runewidthClamp(line, h.width))
}

// shortcutBox is the bordered context line + shortcut grid.
func (h *Header) shortcutBox() string {
	grid := strings.Join(renderShortcutGrid(h.allShortcuts(), shortcutGridRows), "\n")
	inner := lipgloss.JoinVertical(lipgloss.Left, h.contextLine(), grid)
	return headerBoxStyle.Render(inner)
}

func (h *Header) contextLine() string {
	ws := h.activeWS
	if ws == "" {
		ws = "—"
	}
	return strings.Join([]string{
		headerLogoStyle.Render("claude-squad v" + h.version),
		headerLabelStyle.Render("ns:") + headerValueStyle.Render(ws),
		headerLabelStyle.Render("sessions:") + headerValueStyle.Render(fmt.Sprintf("%d", h.sessionCount)),
		headerLabelStyle.Render("workspaces:") + headerValueStyle.Render(fmt.Sprintf("%d", h.workspaceCount)),
	}, "  ")
}

// allShortcuts prepends the navigation primitives to the contextual menu entries.
func (h *Header) allShortcuts() []MenuEntry {
	base := []MenuEntry{
		{Key: ":", Desc: "command"},
		{Key: "/", Desc: "filter"},
	}
	return append(base, h.shortcuts...)
}

// logoBlock renders the ASCII wordmark + tagline, vertically centered to the
// box height so the two top-band columns align.
func (h *Header) logoBlock() string {
	art := logoArtStyle.Render(strings.Join(logoArt, "\n"))
	tag := logoTagStyle.Render("claude-squad")
	logo := lipgloss.JoinVertical(lipgloss.Right, art, tag)
	return lipgloss.Place(lipgloss.Width(logo), headerBoxHeight, lipgloss.Right, lipgloss.Center, logo)
}

// renderShortcutGrid lays the entries out column-major into a fixed number of
// rows (so the header height stays constant regardless of entry count).
func renderShortcutGrid(entries []MenuEntry, rows int) []string {
	if rows < 1 {
		rows = 1
	}
	lines := make([]string, rows)
	n := len(entries)
	if n == 0 {
		return lines
	}
	cols := (n + rows - 1) / rows

	type cell struct {
		s string
		w int
	}
	cells := make([]cell, n)
	for i, e := range entries {
		s := renderShortcutEntry(e)
		cells[i] = cell{s, lipgloss.Width(s)}
	}

	colW := make([]int, cols)
	for c := 0; c < cols; c++ {
		for r := 0; r < rows; r++ {
			if idx := c*rows + r; idx < n && cells[idx].w > colW[c] {
				colW[c] = cells[idx].w
			}
		}
	}

	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			w := 0
			if idx < n {
				b.WriteString(cells[idx].s)
				w = cells[idx].w
			}
			if pad := colW[c] - w; pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
			if c != cols-1 {
				b.WriteString("   ")
			}
		}
		lines[r] = b.String()
	}
	return lines
}

func renderShortcutEntry(e MenuEntry) string {
	key := shortcutBracketStyle.Render("<") + shortcutKeyStyle.Render(e.Key) + shortcutBracketStyle.Render(">")
	desc := shortcutDescStyle.Render(e.Desc)
	if e.Action {
		desc = shortcutActionStyle.Render(e.Desc)
	}
	return key + " " + desc
}

func (h *Header) breadcrumbRow() string {
	var crumb string
	if h.breadcrumb != "" {
		segs := strings.Split(h.breadcrumb, "/")
		rendered := make([]string, len(segs))
		for i, s := range segs {
			rendered[i] = breadcrumbStyle.Render(strings.TrimSpace(s))
		}
		crumb = strings.Join(rendered, breadcrumbSepStyle.Render(" › "))
	}
	if h.filter != "" {
		indicator := filterIndicatorStyle.Render("/" + h.filter)
		if crumb != "" {
			crumb += "   " + indicator
		} else {
			crumb = indicator
		}
	}
	return runewidthClamp(crumb, h.width)
}

// runewidthClamp truncates a (possibly styled) string to width display columns.
// Styled strings are left intact when over width (their content is short by
// construction); only plain strings are hard-truncated.
func runewidthClamp(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if strings.ContainsRune(s, 0x1b) {
		return s
	}
	return truncatePlain(s, width)
}

func truncatePlain(s string, width int) string {
	if width <= 1 {
		return ""
	}
	out := make([]rune, 0, width)
	w := 0
	for _, r := range s {
		if w+1 > width-1 {
			break
		}
		out = append(out, r)
		w++
	}
	return string(out) + "…"
}
