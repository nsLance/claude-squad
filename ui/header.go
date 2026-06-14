package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// headerHeight is the fixed number of rows the header occupies: a banner row
// and a breadcrumb row.
const headerHeight = 2

var headerLogoStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var headerLabelStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var headerValueStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var headerHintKeyStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var headerHintDescStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

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

// hint is a single key/description pair shown on the right of the banner.
type hint struct{ key, desc string }

var defaultHints = []hint{
	{":", "cmd"},
	{"/", "filter"},
	{"↵", "open"},
	{"esc", "back"},
	{"?", "help"},
	{"q", "quit"},
}

// Header renders the k9s-style top banner (context block + hotkey hints) and a
// breadcrumb line for the current view. It holds no application state of its
// own; callers refresh its fields each frame via Update.
type Header struct {
	width int

	version        string
	activeWS       string
	sessionCount   int
	workspaceCount int
	breadcrumb     string
	filter         string
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

func (h *Header) String() string {
	if h.width <= 0 {
		return strings.Repeat("\n", headerHeight-1)
	}
	return lipgloss.JoinVertical(lipgloss.Left, h.bannerRow(), h.breadcrumbRow())
}

func (h *Header) bannerRow() string {
	ws := h.activeWS
	if ws == "" {
		ws = "—"
	}
	left := strings.Join([]string{
		headerLogoStyle.Render("claude-squad v" + h.version),
		headerLabelStyle.Render("ns:") + headerValueStyle.Render(ws),
		headerLabelStyle.Render("sessions:") + headerValueStyle.Render(fmt.Sprintf("%d", h.sessionCount)),
		headerLabelStyle.Render("workspaces:") + headerValueStyle.Render(fmt.Sprintf("%d", h.workspaceCount)),
	}, "  ")

	right := h.renderHints()

	gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Not enough room for both — keep the context block, drop the hints.
		return runewidthClamp(left, h.width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (h *Header) renderHints() string {
	parts := make([]string, 0, len(defaultHints))
	for _, hi := range defaultHints {
		parts = append(parts, headerHintKeyStyle.Render(hi.key)+" "+headerHintDescStyle.Render(hi.desc))
	}
	return strings.Join(parts, "  ")
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
