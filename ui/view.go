package ui

// ViewKind identifies the screens in the navigation stack.
type ViewKind int

const (
	ViewWorkspaces ViewKind = iota
	ViewSessions
	ViewSessionDetail
)

// View is one screen in the drill-down navigation stack. Concrete views are
// thin wrappers around the persistent List / TabbedWindow / workspaces table so
// navigation never reallocates or resizes the backing components.
type View interface {
	// Kind reports which screen this is (drives key routing in the app).
	Kind() ViewKind
	// Breadcrumb returns this view's segment of the header breadcrumb.
	Breadcrumb() string
	// SetSize sets the content-region dimensions for this view.
	SetSize(width, height int)
	// String renders the view's content region.
	String() string
}
