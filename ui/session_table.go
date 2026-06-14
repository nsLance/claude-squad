package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"

	"github.com/charmbracelet/lipgloss"
)

// csTableUI reports whether the k9s-style columnar table renderer is enabled.
// Phase A ships it behind CS_TABLE_UI so the classic list stays the default
// until the full table-primary layout lands.
func csTableUI() bool {
	return os.Getenv("CS_TABLE_UI") != ""
}

var runningStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#2b8acb", Dark: "#4ab3ff"})

var loadingStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

var diffZeroStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

// statusCell renders a session's status as a short colored label for a table
// cell (no animated spinner — the table favors a static, scannable glyph).
func statusCell(i *session.Instance) string {
	switch i.Status {
	case session.Running:
		return runningStyle.Render("▶ running")
	case session.Loading:
		return loadingStyle.Render("… loading")
	case session.Ready:
		return readyStyle.Render("● ready")
	case session.Paused:
		return pausedStyle.Render("⏸ paused")
	case session.Exited:
		return exitedStyle.Render("✗ exited")
	}
	return ""
}

// statusRank orders statuses for sorting (active sessions first).
func statusRank(s session.Status) int {
	switch s {
	case session.Running:
		return 0
	case session.Loading:
		return 1
	case session.Ready:
		return 2
	case session.Paused:
		return 3
	case session.Exited:
		return 4
	}
	return 5
}

// diffCell renders a session's +added/-removed diff stats as a colored cell.
func diffCell(i *session.Instance) string {
	s := i.GetDiffStats()
	if s == nil || s.Error != nil || s.IsEmpty() {
		return diffZeroStyle.Render("—")
	}
	return addedLinesStyle.Render(fmt.Sprintf("+%d", s.Added)) +
		" " + removedLinesStyle.Render(fmt.Sprintf("-%d", s.Removed))
}

// diffMagnitude is the sort key for the DIFF column.
func diffMagnitude(i *session.Instance) int {
	s := i.GetDiffStats()
	if s == nil || s.Error != nil || s.IsEmpty() {
		return 0
	}
	return s.Added + s.Removed
}

// humanizeAge renders a compact age like "8s", "12m", "3h", "5d".
func humanizeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// branchLabel returns the branch, suffixed with the repo name when multiple
// repos are in play (mirrors the classic renderer).
func branchLabel(i *session.Instance, hasMultipleRepos bool) string {
	branch := i.Branch
	if i.Started() && hasMultipleRepos {
		if repoName, err := i.RepoName(); err != nil {
			log.ErrorLog.Printf("could not get repo name in table renderer: %v", err)
		} else {
			branch += fmt.Sprintf(" (%s)", repoName)
		}
	}
	return branch
}

// SessionColumns builds the column set for the sessions table. showWorkspace
// drops the WORKSPACE column when the view is already scoped to one workspace
// (it would be redundant). reg resolves workspace ids to display names.
func SessionColumns(reg *config.WorkspaceRegistry, hasMultipleRepos, showWorkspace bool) []Column {
	inst := func(row any) *session.Instance { return row.(*session.Instance) }

	cols := []Column{
		{
			Title: "NAME", MinWidth: 12, Weight: 3, Align: lipgloss.Left,
			Render: func(r any) string { return inst(r).Title },
			Less:   func(a, b any) bool { return inst(a).Title < inst(b).Title },
		},
	}
	if showWorkspace {
		cols = append(cols, Column{
			Title: "WORKSPACE", MinWidth: 8, Weight: 2, Align: lipgloss.Left,
			Render: func(r any) string { return workspaceLabel(reg, inst(r).WorkspaceID) },
			Less: func(a, b any) bool {
				return workspaceLabel(reg, inst(a).WorkspaceID) < workspaceLabel(reg, inst(b).WorkspaceID)
			},
		})
	}
	cols = append(cols,
		Column{
			Title: "BRANCH", MinWidth: 10, Weight: 3, Align: lipgloss.Left,
			Render: func(r any) string { return branchLabel(inst(r), hasMultipleRepos) },
			Less:   func(a, b any) bool { return inst(a).Branch < inst(b).Branch },
		},
		Column{
			Title: "STATUS", MinWidth: 9, Weight: 0, Align: lipgloss.Left,
			Render: func(r any) string { return statusCell(inst(r)) },
			Less:   func(a, b any) bool { return statusRank(inst(a).Status) < statusRank(inst(b).Status) },
		},
		Column{
			Title: "DIFF", MinWidth: 8, Weight: 0, Align: lipgloss.Right,
			Render: func(r any) string { return diffCell(inst(r)) },
			Less:   func(a, b any) bool { return diffMagnitude(inst(a)) < diffMagnitude(inst(b)) },
		},
		Column{
			Title: "AGE", MinWidth: 4, Weight: 0, Align: lipgloss.Right,
			Render: func(r any) string { return humanizeAge(inst(r).CreatedAt) },
			Less:   func(a, b any) bool { return inst(a).CreatedAt.After(inst(b).CreatedAt) },
		},
	)
	return cols
}

// visibleRows returns the instances passing the current view filter, plus the
// index of the selected instance within that slice (-1 if not visible).
func (l *List) visibleRows() (rows []any, selIdx int) {
	sel := l.GetSelectedInstance()
	selIdx = -1
	for _, it := range l.items {
		if l.viewFilter != "" && it.WorkspaceID != l.viewFilter {
			continue
		}
		if it == sel {
			selIdx = len(rows)
		}
		rows = append(rows, it)
	}
	return rows, selIdx
}

// RenderTable renders the session list as a k9s-style columnar table under the
// existing list header. Used when csTableUI() is enabled (Phase A); Phase D
// promotes it to the primary renderer via SessionsView.
func (l *List) RenderTable() string {
	header := l.renderHeader()
	headerLines := strings.Split(header, "\n")

	if len(l.items) == 0 {
		body := l.renderEmptyState()
		return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top,
			strings.Join(append(headerLines, strings.Split(body, "\n")...), "\n"))
	}

	rows, selIdx := l.visibleRows()
	reg := config.LoadWorkspaceRegistry()
	showWorkspace := l.viewFilter == ""
	t := NewTable(SessionColumns(reg, len(l.repos) > 1, showWorkspace))
	t.SetRows(rows)
	if selIdx >= 0 {
		t.SetSelected(selIdx)
	}
	t.SetSize(l.width, l.height-len(headerLines))

	bodyLines := strings.Split(t.String(), "\n")
	final := append(headerLines, bodyLines...)
	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, strings.Join(final, "\n"))
}
