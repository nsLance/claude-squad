package ui

import "fmt"

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
	scope := v.scopeLabel
	if scope == "" {
		scope = "all"
	}
	title := fmt.Sprintf("Sessions(%s)[%d]", scope, v.list.VisibleCount())
	// Render the table into the box interior (width-2 x height-2); the box pads
	// to the full content region so overlays center correctly and the bottom
	// bar/error row sit at the bottom of the screen.
	// width-3: interior is width-2, less 1 for a right gutter the box pads back.
	body := v.list.RenderTableBody(v.width-3, v.height-2)
	return renderContentBox(title, body, v.width, v.height)
}
