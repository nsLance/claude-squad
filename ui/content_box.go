package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
)

var contentBoxBorderStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("62"))

var contentBoxTitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#ffffff"})

// renderContentBox draws a rounded border of exactly width x height around body,
// with a k9s-style title embedded in the top-left of the top border (e.g.
// "╭─ Workspaces[5] ───╮"). body is laid into the interior (width-2 x height-2),
// padded with blanks and ANSI-aware clipped so the box dimensions stay exact.
func renderContentBox(title, body string, width, height int) string {
	if width < 2 || height < 2 {
		return body
	}
	innerW := width - 2
	innerH := height - 2

	var b strings.Builder
	b.WriteString(topBorder(title, width))
	b.WriteByte('\n')

	bodyLines := strings.Split(body, "\n")
	for i := 0; i < innerH; i++ {
		line := ""
		if i < len(bodyLines) {
			line = bodyLines[i]
		}
		line = truncate.String(line, uint(innerW))
		if pad := innerW - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		b.WriteString(contentBoxBorderStyle.Render("│"))
		b.WriteString(line)
		b.WriteString(contentBoxBorderStyle.Render("│"))
		b.WriteByte('\n')
	}

	b.WriteString(contentBoxBorderStyle.Render("╰" + strings.Repeat("─", innerW) + "╯"))
	return b.String()
}

// topBorder builds the rounded top edge with an optional embedded title. Falls
// back to a plain edge when the title can't fit.
func topBorder(title string, width int) string {
	plain := contentBoxBorderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	if title == "" {
		return plain
	}
	styled := contentBoxTitleStyle.Render(title)
	// "╭" + "─ " + title + " " + dashes + "╮"  must total `width` columns.
	fill := width - 5 - lipgloss.Width(styled)
	if fill < 0 {
		return plain
	}
	return contentBoxBorderStyle.Render("╭─ ") +
		styled +
		contentBoxBorderStyle.Render(" "+strings.Repeat("─", fill)+"╮")
}
