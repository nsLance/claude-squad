package ui

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// WorkspaceRow is one row of the workspaces table. The app builds these from
// the registry + live session counts and hands them to the view.
type WorkspaceRow struct {
	ID       string
	Name     string
	Repo     string
	Sessions int
	LastUsed time.Time
}

// WorkspaceColumns is the column set for the workspaces ("namespaces") table.
func WorkspaceColumns() []Column {
	row := func(r any) WorkspaceRow { return r.(WorkspaceRow) }
	return []Column{
		{
			Title: "NAME", MinWidth: 14, Weight: 2, Align: lipgloss.Left,
			Render: func(r any) string { return row(r).Name },
			Less:   func(a, b any) bool { return row(a).Name < row(b).Name },
		},
		{
			Title: "REPO", MinWidth: 16, Weight: 3, Align: lipgloss.Left,
			Render: func(r any) string { return repoTail(row(r).Repo) },
			Less:   func(a, b any) bool { return row(a).Repo < row(b).Repo },
		},
		{
			Title: "SESSIONS", MinWidth: 8, Weight: 0, Align: lipgloss.Right,
			Render: func(r any) string { return fmt.Sprintf("%d", row(r).Sessions) },
			Less:   func(a, b any) bool { return row(a).Sessions < row(b).Sessions },
		},
		{
			Title: "LAST USED", MinWidth: 9, Weight: 0, Align: lipgloss.Right,
			Render: func(r any) string {
				w := row(r)
				if w.LastUsed.IsZero() {
					return "—"
				}
				return humanizeAge(w.LastUsed)
			},
			Less: func(a, b any) bool { return row(a).LastUsed.After(row(b).LastUsed) },
		},
	}
}

// repoTail shortens a repo path to its last two segments for display.
func repoTail(path string) string {
	if path == "" {
		return "—"
	}
	dir, base := filepath.Split(filepath.Clean(path))
	parent := filepath.Base(filepath.Clean(dir))
	if parent == "." || parent == string(filepath.Separator) || parent == "" {
		return base
	}
	return filepath.Join(parent, base)
}

// WorkspacesView renders the workspaces ("namespaces") table. It owns its own
// Table + selection, independent of the sessions list.
type WorkspacesView struct {
	table         *Table
	width, height int
}

func NewWorkspacesView() *WorkspacesView {
	return &WorkspacesView{table: NewTable(WorkspaceColumns())}
}

// SetRows refreshes the table, preserving the selection index across refreshes.
func (v *WorkspacesView) SetRows(rows []WorkspaceRow) {
	anyRows := make([]any, len(rows))
	for i, r := range rows {
		anyRows[i] = r
	}
	sel := v.table.Selected()
	v.table.SetRows(anyRows)
	v.table.SetSelected(sel)
}

// SelectedWorkspaceID returns the id of the highlighted workspace, or "".
func (v *WorkspacesView) SelectedWorkspaceID() string {
	if r := v.table.SelectedRow(); r != nil {
		return r.(WorkspaceRow).ID
	}
	return ""
}

func (v *WorkspacesView) MoveUp()    { v.table.MoveUp() }
func (v *WorkspacesView) MoveDown()  { v.table.MoveDown() }
func (v *WorkspacesView) CycleSort() { v.table.CycleSort() }

// SetFilter applies a live substring filter to the workspaces table.
func (v *WorkspacesView) SetFilter(f string) { v.table.SetFilter(f) }

func (v *WorkspacesView) Kind() ViewKind     { return ViewWorkspaces }
func (v *WorkspacesView) Breadcrumb() string { return "workspaces" }

func (v *WorkspacesView) SetSize(width, height int) {
	v.width, v.height = width, height
	v.table.SetSize(width, height)
}

func (v *WorkspacesView) String() string {
	v.table.SetSize(v.width, v.height)
	// Pad to the full content height (see SessionsView.String) so the layout
	// fills the screen and overlays center correctly.
	return lipgloss.Place(v.width, v.height, lipgloss.Left, lipgloss.Top, v.table.String())
}
