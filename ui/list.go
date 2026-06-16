package ui

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

var emptyHintStyle = lipgloss.NewStyle().
	Padding(2, 2).
	Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})

var readyStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var addedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var removedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#de613e"))

var pausedStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

// exitedStyle marks a session whose agent process has exited (tmux gone).
var exitedStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#de613e"))

type List struct {
	items         []*session.Instance
	selectedIdx   int
	height, width int
	autoyes       bool

	// activeWorkspaceName / activeWorkspaceID: where new sessions land (driven by W key).
	activeWorkspaceName string
	activeWorkspaceID   string

	// viewFilter is the workspace id to filter the visible list to. Empty = show all.
	viewFilter string

	// textFilter is a live "/"-filter substring matched against each session's
	// title and branch. Empty = no text filter.
	textFilter string

	// collapsedWorkspaces tracks which workspace groups are folded in the list.
	collapsedWorkspaces map[string]bool

	// map of repo name to number of instances using it. Used to display the repo name only if there are
	// multiple repos in play.
	repos map[string]int
}

// SetActiveWorkspace sets the display name shown in the title bar to indicate
// which workspace new sessions will be created in.
func (l *List) SetActiveWorkspace(name string) {
	l.activeWorkspaceName = name
}

// SetActiveWorkspaceID records the id of the workspace new sessions land in.
// Used to detect the "active" tab in the workspace tab bar.
func (l *List) SetActiveWorkspaceID(id string) {
	l.activeWorkspaceID = id
}

// SetViewFilter restricts the rendered list to instances belonging to the given workspace.
// Empty string means show all.
func (l *List) SetViewFilter(workspaceID string) {
	l.viewFilter = workspaceID
	// Snap selection to a visible item if the current one became hidden.
	l.ensureSelectionVisible()
}

// GetViewFilter returns the current view-filter workspace id, or "" if showing all.
func (l *List) GetViewFilter() string {
	return l.viewFilter
}

// SetTextFilter applies a live substring filter (matched against title/branch)
// and snaps the selection to a still-visible item.
func (l *List) SetTextFilter(s string) {
	l.textFilter = s
	l.ensureSelectionVisible()
}

// GetTextFilter returns the active "/"-filter substring, or "".
func (l *List) GetTextFilter() string {
	return l.textFilter
}

// VisibleInstanceCount returns the number of instances matching the current view filter
// (ignoring collapse — collapsed items are "hidden" visually but still counted).
func (l *List) VisibleInstanceCount() int {
	if l.viewFilter == "" {
		return len(l.items)
	}
	n := 0
	for _, inst := range l.items {
		if inst.WorkspaceID == l.viewFilter {
			n++
		}
	}
	return n
}

// WorkspaceCount returns the number of distinct workspaces present in the list.
func (l *List) WorkspaceCount() int {
	seen := map[string]struct{}{}
	for _, inst := range l.items {
		seen[inst.WorkspaceID] = struct{}{}
	}
	return len(seen)
}

// isItemVisible reports whether the given instance is currently rendered (i.e.
// passes the workspace view filter, the "/" text filter, and is not inside a
// collapsed group).
func (l *List) isItemVisible(inst *session.Instance) bool {
	if l.viewFilter != "" && inst.WorkspaceID != l.viewFilter {
		return false
	}
	if l.collapsedWorkspaces[inst.WorkspaceID] {
		return false
	}
	if l.textFilter != "" && !sessionMatchesFilter(inst, l.textFilter) {
		return false
	}
	return true
}

// sessionMatchesFilter reports whether an instance matches the "/" filter
// (case-insensitive substring against its title and branch).
func sessionMatchesFilter(inst *session.Instance, filter string) bool {
	needle := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(inst.Title), needle) ||
		strings.Contains(strings.ToLower(inst.Branch), needle)
}

// ensureSelectionVisible nudges the selection forward (then backward) to the
// nearest visible item if the current selection has been hidden by filter/collapse.
func (l *List) ensureSelectionVisible() {
	if len(l.items) == 0 {
		return
	}
	if l.selectedIdx < len(l.items) && l.isItemVisible(l.items[l.selectedIdx]) {
		return
	}
	for i := l.selectedIdx + 1; i < len(l.items); i++ {
		if l.isItemVisible(l.items[i]) {
			l.selectedIdx = i
			return
		}
	}
	for i := l.selectedIdx - 1; i >= 0; i-- {
		if l.isItemVisible(l.items[i]) {
			l.selectedIdx = i
			return
		}
	}
}

// NewList constructs a List. The spinner is retained for API compatibility with
// callers; status is now rendered statically in the table.
func NewList(_ *spinner.Model, autoYes bool) *List {
	return &List{
		items:               []*session.Instance{},
		repos:               make(map[string]int),
		collapsedWorkspaces: map[string]bool{},
		autoyes:             autoYes,
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
}

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	for i, item := range l.items {
		if !item.Started() || item.Paused() || item.Status == session.Exited {
			continue
		}

		if innerErr := item.SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}

func (l *List) NumInstances() int {
	return len(l.items)
}

// renderEmptyState produces the body for an empty list — workspace-aware so
// users see what to do next instead of a bare "No sessions yet."
func (l *List) renderEmptyState() string {
	if l.activeWorkspaceName != "" {
		return emptyHintStyle.Render(fmt.Sprintf(
			"No sessions in %q yet.\n\n  n   create a new session here\n  A   add another workspace\n  W   switch which workspace new sessions land in\n  ?   show help",
			l.activeWorkspaceName,
		))
	}
	return emptyHintStyle.Render(
		"No sessions yet.\n\n  A   add a workspace (existing or new directory)\n  ?   show help",
	)
}

// scrollClamp returns the slice of body lines that should be visible given an
// available height. The window is positioned so the selected item is roughly
// centered, then clamped to [0, len(lines)-available].
func scrollClamp(lines []string, selectedLine, available int) []string {
	if available <= 0 || len(lines) <= available {
		return lines
	}
	if selectedLine < 0 {
		return lines[:available]
	}
	half := available / 2
	offset := selectedLine - half
	if offset < 0 {
		offset = 0
	}
	if offset+available > len(lines) {
		offset = len(lines) - available
	}
	return lines[offset : offset+available]
}

const unknownWorkspaceLabel = "(unknown workspace)"

func workspaceLabel(reg *config.WorkspaceRegistry, id string) string {
	if id == "" {
		return unknownWorkspaceLabel
	}
	if ws := reg.Get(id); ws != nil {
		return ws.DisplayName
	}
	return unknownWorkspaceLabel
}

// Down selects the next visible item in the list (skipping items hidden by the
// current view filter or by a collapsed workspace group).
func (l *List) Down() {
	if len(l.items) == 0 {
		return
	}
	for i := l.selectedIdx + 1; i < len(l.items); i++ {
		if l.isItemVisible(l.items[i]) {
			l.selectedIdx = i
			return
		}
	}
}

// Kill selects the next item in the list.
// KillSelected tears down the selected instance (tmux session + git worktree)
// but leaves the row in the list. It returns the teardown error so callers can
// decide whether to drop the row: a failed worktree removal should stay visible
// and recoverable rather than silently vanish into an orphan. Use RemoveSelected
// once the instance is confirmed dead.
func (l *List) KillSelected() error {
	if len(l.items) == 0 {
		return nil
	}
	return l.items[l.selectedIdx].Kill()
}

// RemoveSelected unregisters the selected instance's repo and splices it out of
// the list. Call only after the instance has been torn down (KillSelected).
func (l *List) RemoveSelected() {
	if len(l.items) == 0 {
		return
	}
	targetInstance := l.items[l.selectedIdx]

	// Unregister the reponame.
	repoName, err := targetInstance.RepoName()
	if err != nil {
		log.ErrorLog.Printf("could not get repo name: %v", err)
	} else {
		l.rmRepo(repoName)
	}

	// Splice the item out, then clamp selectedIdx so it remains in range. If
	// the removed item was the last one, selectedIdx now points past the end —
	// step it back. This must happen unconditionally rather than relying on
	// Up()'s side effect, because Up() only moves the cursor when it finds a
	// visible item below; if none qualifies (everything filtered/collapsed) it
	// leaves selectedIdx stale and the next GetSelectedInstance panics.
	l.items = append(l.items[:l.selectedIdx], l.items[l.selectedIdx+1:]...)
	if l.selectedIdx >= len(l.items) {
		l.selectedIdx = len(l.items) - 1
	}
	if l.selectedIdx < 0 {
		l.selectedIdx = 0
	}
	l.ensureSelectionVisible()
}

// Kill tears down the selected instance and removes it from the list,
// best-effort: a teardown error is logged but the row is dropped regardless.
// Used by the unstarted/failed-create cleanup paths where the instance was never
// persisted, so there's nothing to recover. The interactive "kill this session"
// path uses KillSelected + RemoveSelected so a failed removal stays recoverable.
func (l *List) Kill() {
	if err := l.KillSelected(); err != nil {
		log.ErrorLog.Printf("could not kill instance: %v", err)
	}
	l.RemoveSelected()
}

func (l *List) Attach() (chan struct{}, error) {
	targetInstance := l.items[l.selectedIdx]
	return targetInstance.Attach()
}

// Up selects the prev visible item in the list (skipping items hidden by filter or collapse).
func (l *List) Up() {
	if len(l.items) == 0 {
		return
	}
	for i := l.selectedIdx - 1; i >= 0; i-- {
		if l.isItemVisible(l.items[i]) {
			l.selectedIdx = i
			return
		}
	}
}

func (l *List) addRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		l.repos[repo] = 0
	}
	l.repos[repo]++
}

func (l *List) rmRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		log.ErrorLog.Printf("repo %s not found", repo)
		return
	}
	l.repos[repo]--
	if l.repos[repo] == 0 {
		delete(l.repos, repo)
	}
}

// AddInstance adds a new instance to the list. It returns a finalizer function that should be called when the instance
// is started. If the instance was restored from storage or is paused, you can call the finalizer immediately.
// When creating a new one and entering the name, you want to call the finalizer once the name is done.
func (l *List) AddInstance(instance *session.Instance) (finalize func()) {
	l.items = append(l.items, instance)
	// The finalizer registers the repo name once the instance is started.
	return func() {
		repoName, err := instance.RepoName()
		if err != nil {
			log.ErrorLog.Printf("could not get repo name: %v", err)
			return
		}

		l.addRepo(repoName)
	}
}

// GetSelectedInstance returns the currently selected instance. Returns nil if
// the list is empty or selectedIdx is somehow out of range (a defensive guard:
// list mutations like Kill must keep selectedIdx in-bounds, but a missed clamp
// must not crash the program).
func (l *List) GetSelectedInstance() *session.Instance {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return nil
	}
	return l.items[l.selectedIdx]
}

// SetSelectedInstance sets the selected index. Noop if the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx >= len(l.items) {
		return
	}
	l.selectedIdx = idx
}

// SelectInstance finds and selects the given instance in the list.
func (l *List) SelectInstance(target *session.Instance) {
	for i, inst := range l.items {
		if inst == target {
			l.SetSelectedInstance(i)
			return
		}
	}
}

// MoveUp swaps the selected instance with the one above it.
func (l *List) MoveUp() bool {
	if l.selectedIdx <= 0 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx-1] = l.items[l.selectedIdx-1], l.items[l.selectedIdx]
	l.selectedIdx--
	return true
}

// MoveDown swaps the selected instance with the one below it.
func (l *List) MoveDown() bool {
	if l.selectedIdx >= len(l.items)-1 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx+1] = l.items[l.selectedIdx+1], l.items[l.selectedIdx]
	l.selectedIdx++
	return true
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items
}
