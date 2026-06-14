package ui

// SessionDetailView renders the preview/diff/terminal detail for the selected
// session. It borrows a pointer to the app's single persistent TabbedWindow
// (never a copy) so tmux preview sizing stays stable across navigation. The
// title is read lazily so the breadcrumb tracks the current selection.
type SessionDetailView struct {
	tw         *TabbedWindow
	scopeLabel string
	titleFn    func() string
}

func NewSessionDetailView(tw *TabbedWindow, titleFn func() string) *SessionDetailView {
	return &SessionDetailView{tw: tw, titleFn: titleFn}
}

// SetScopeLabel records the parent workspace for the breadcrumb.
func (v *SessionDetailView) SetScopeLabel(label string) { v.scopeLabel = label }

func (v *SessionDetailView) Kind() ViewKind { return ViewSessionDetail }

func (v *SessionDetailView) Breadcrumb() string {
	// Just the session title — the parent sessions(<ws>) segment already carries
	// the workspace scope, so prefixing it here would render it twice.
	if v.titleFn != nil {
		return v.titleFn()
	}
	return ""
}

// SetSize forwards to the shared TabbedWindow. The app sizes the TabbedWindow
// centrally too (with the same value) so this never causes a resize that the
// central sizing wouldn't already apply.
func (v *SessionDetailView) SetSize(width, height int) { v.tw.SetSize(width, height) }

func (v *SessionDetailView) String() string { return v.tw.String() }
