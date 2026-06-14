package ui

import "github.com/charmbracelet/lipgloss"

// SessionsView renders the sessions table full-width (the table-primary root or
// a workspace-scoped list). It wraps the persistent *List so all navigation and
// session actions continue to operate on the same selection state.
type SessionsView struct {
	list          *List
	scopeLabel    string // workspace display name, or "" for "all"
	width, height int
}

func NewSessionsView(list *List) *SessionsView {
	return &SessionsView{list: list}
}

// SetScopeLabel records the workspace this view is scoped to (for the
// breadcrumb). Empty means unscoped ("all").
func (v *SessionsView) SetScopeLabel(label string) { v.scopeLabel = label }

func (v *SessionsView) Kind() ViewKind { return ViewSessions }

func (v *SessionsView) Breadcrumb() string {
	if v.scopeLabel == "" || v.scopeLabel == "All" {
		return "sessions(all)"
	}
	return "sessions(" + v.scopeLabel + ")"
}

func (v *SessionsView) SetSize(width, height int) { v.width, v.height = width, height }

func (v *SessionsView) String() string {
	// Pad to the full content height so the bottom menu/error bar sit at the
	// bottom of the screen and overlays (centered on the rendered view) aren't
	// clipped against a short background.
	return lipgloss.Place(v.width, v.height, lipgloss.Left, lipgloss.Top,
		v.list.RenderTableBody(v.width, v.height))
}
