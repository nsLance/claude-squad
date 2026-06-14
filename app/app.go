package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool, workspaceID string) error {
	p := tea.NewProgram(
		newHome(ctx, program, autoYes, workspaceID),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateAddWorkspace is the state while the "add workspace" overlay is open.
	stateAddWorkspace
	// stateCommand is the state while the ":" command bar is capturing input.
	stateCommand
	// stateFilter is the state while the "/" filter bar is capturing input.
	stateFilter
)

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// instanceStarting is true while a background instance start is in progress.
	// Prevents double-submission and guards against interacting with a not-yet-started instance.
	instanceStarting bool
	// startingInstance holds a reference to the instance being started in the background.
	startingInstance *session.Instance

	// workspaceID is the active workspace; new instances inherit it.
	workspaceID string

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// header displays the top banner (context block + hotkeys) and breadcrumb
	header *ui.Header
	// menu displays the bottom menu
	menu *ui.Menu
	// cmdBar is the ":" command-bar input line (shown only in stateCommand)
	cmdBar *ui.CommandBar
	// filterBar is the "/" filter input line (shown only in stateFilter)
	filterBar *ui.CommandBar
	// filterText is the active "/" filter, applied to the current view's table.
	// Non-empty even after the input is committed (Enter); cleared by Esc.
	filterText string

	// -- Navigation stack (k9s-style drill-down) --

	// viewStack is never empty; the bottom is the root table view. The top is
	// the currently-rendered screen.
	viewStack []ui.View
	// The three concrete views are persistent (never reallocated) so navigation
	// doesn't resize the backing List/TabbedWindow. sessionsView/sessionDetailView
	// wrap m.list/m.tabbedWindow; workspacesView owns its own table.
	workspacesView    *ui.WorkspacesView
	sessionsView      *ui.SessionsView
	sessionDetailView *ui.SessionDetailView
	// lastWindowSize caches the most recent terminal size so relayout() can
	// re-run the size math after a push/pop changes the active view.
	lastWindowSize tea.WindowSizeMsg
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
	// pendingConfirmCmd is dispatched after the confirmation overlay closes so
	// that flows which want to emit tea.Msgs from a confirm decision (errBox
	// auto-hide, follow-up state transitions) can do so via the message loop
	// rather than mutating m synchronously inside an OnConfirm callback.
	pendingConfirmCmd tea.Cmd
}

// sessionPath returns the git-repo path a new session should be rooted at.
// When an active workspace is set we use its registered RepoPath (so cs-edge
// can be launched anywhere — cwd doesn't have to be the repo). When no
// workspace is active, we fall back to "." (the current behavior); the caller
// should refuse to create a session in that case via requireWorkspace.
func (m *home) sessionPath() string {
	if m.workspaceID == "" {
		return "."
	}
	reg := config.LoadWorkspaceRegistry()
	if ws := reg.Get(m.workspaceID); ws != nil && ws.RepoPath != "" {
		return ws.RepoPath
	}
	return "."
}

// requireWorkspace returns an error tea.Cmd refusing a new-session action when
// not scoped to a workspace (e.g. on the unscoped "all sessions" view). Returns
// nil once a workspace is in context. New sessions are created from inside a
// workspace — enter one first.
func (m *home) requireWorkspace() tea.Cmd {
	if m.workspaceID != "" {
		return nil
	}
	return m.handleError(fmt.Errorf("enter a workspace first (↵ on the workspaces list), then press n to create a session there"))
}

// addWorkspaceFromPath handles a path the user typed into the Add-Workspace
// overlay. It DTRTs across three cases:
//   - path doesn't exist → mkdir -p, git init, empty initial commit
//   - path exists but isn't a git repo → return a helpful error (we don't
//     mutate existing directories implicitly)
//   - path exists and is a git repo → register and switch to it
//
// On success the new workspace becomes the active one.
func (m *home) addWorkspaceFromPath(rawPath string) error {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(rawPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("expand ~: %w", err)
		}
		rawPath = filepath.Join(home, rawPath[1:])
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(abs)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(abs, 0755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if out, err := exec.Command("git", "-C", abs, "init", "-q").CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %w\n%s", err, out)
		}
	case err != nil:
		return fmt.Errorf("stat %s: %w", abs, err)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", abs)
	case !git.IsGitRepo(abs):
		return fmt.Errorf("%s exists but isn't a git repository — `git init` it first or pick a different path", abs)
	}

	// cs creates worktrees off HEAD; a freshly-init'd repo has no commits and
	// `git worktree add` fails. Plant an empty initial commit if needed — works
	// regardless of whether we created the repo ourselves or the user did.
	if err := exec.Command("git", "-C", abs, "rev-parse", "HEAD").Run(); err != nil {
		if out, err := exec.Command("git", "-C", abs, "commit", "-q", "--allow-empty", "-m", "Initial commit").CombinedOutput(); err != nil {
			return fmt.Errorf("initial commit: %w\n%s", err, out)
		}
	}

	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		canonical = abs
	}
	reg := config.LoadWorkspaceRegistry()
	ws, err := reg.EnsureWorkspace(canonical, git.FirstRemoteURL(canonical))
	if err != nil {
		return fmt.Errorf("register workspace: %w", err)
	}

	m.workspaceID = ws.ID
	m.list.SetActiveWorkspace(ws.DisplayName)
	m.list.SetActiveWorkspaceID(ws.ID)
	return nil
}

// resolveWorkspaceProgram returns the program command and profile name a new
// session in the active workspace should use. If the active workspace defines
// any profiles, the first one is treated as the workspace default; otherwise
// the caller's launch-time program (m.program) is used and the profile name
// is empty.
func (m *home) resolveWorkspaceProgram() (program, profileName string) {
	program = m.program
	if m.workspaceID == "" {
		return
	}
	reg := config.LoadWorkspaceRegistry()
	ws := reg.Get(m.workspaceID)
	if ws == nil || len(ws.Profiles) == 0 {
		return
	}
	return ws.Profiles[0].Program, ws.Profiles[0].Name
}

// applyWorkspaceFocus updates both the view filter and the active workspace
// (i.e. the "new sessions land here" target) in one shot. Used by V so cycling
// the view and switching the new-session target stay aligned.
func (m *home) applyWorkspaceFocus(id string) {
	m.list.SetViewFilter(id)
	m.workspaceID = id
	reg := config.LoadWorkspaceRegistry()
	if ws := reg.Get(id); ws != nil {
		m.list.SetActiveWorkspace(ws.DisplayName)
		m.list.SetActiveWorkspaceID(ws.ID)
	} else {
		m.list.SetActiveWorkspace("(unknown workspace)")
		m.list.SetActiveWorkspaceID(id)
	}
}

// labelForFilter resolves a workspace id to its display name. Returns "(unknown
// workspace)" for orphan ids (instances pointing to a workspace that's been
// removed from the registry).
func (m *home) labelForFilter(id string) string {
	if id == "" {
		return "All"
	}
	reg := config.LoadWorkspaceRegistry()
	if ws := reg.Get(id); ws != nil {
		return ws.DisplayName
	}
	return "(unknown workspace)"
}

func newHome(ctx context.Context, program string, autoYes bool, workspaceID string) *home {
	// Load application config
	appConfig := config.LoadConfig()

	// Load application state
	appState := config.LoadState()

	// Initialize storage
	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	h := &home{
		ctx:          ctx,
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		header:       ui.NewHeader(),
		menu:         ui.NewMenu(),
		cmdBar:       ui.NewCommandBar(),
		filterBar:    ui.NewBarWithPrompt("/"),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		storage:      storage,
		appConfig:    appConfig,
		program:      program,
		autoYes:      autoYes,
		state:        stateDefault,
		appState:     appState,
		workspaceID:  workspaceID,
	}
	h.list = ui.NewList(&h.spinner, autoYes)

	// Seed the list with the active workspace's display name so the title bar
	// shows it from launch (and `W` to switch is discoverable in the menu).
	if workspaceID != "" {
		reg := config.LoadWorkspaceRegistry()
		if ws := reg.Get(workspaceID); ws != nil {
			h.list.SetActiveWorkspace(ws.DisplayName)
			h.list.SetActiveWorkspaceID(ws.ID)
		}
	}

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Add loaded instances to the list
	for _, instance := range instances {
		// Call the finalizer immediately.
		h.list.AddInstance(instance)()
		if autoYes {
			instance.AutoYes = true
		}
	}

	// Build the navigation views (persistent; the stack holds pointers to them).
	h.workspacesView = ui.NewWorkspacesView()
	h.sessionsView = ui.NewSessionsView(h.list)
	h.sessionsView.SetScopeLabel(h.labelForFilter(workspaceID))
	h.sessionDetailView = ui.NewSessionDetailView(h.tabbedWindow, func() string {
		if sel := h.list.GetSelectedInstance(); sel != nil {
			return sel.Title
		}
		return ""
	})

	// Root view: a workspaces "namespace" list when more than one workspace is
	// registered, otherwise drop straight into that workspace's sessions
	// (preserves the single-workspace landing experience).
	reg := config.LoadWorkspaceRegistry()
	if len(reg.Workspaces) > 1 {
		h.viewStack = []ui.View{h.workspacesView}
	} else {
		h.viewStack = []ui.View{h.sessionsView}
	}

	return h
}

// sessionActionKeys are keybindings that operate on the selected session and so
// only make sense on the sessions / detail views, not the workspaces list. New
// (n) and Kill (D) are intentionally absent — they are context-aware and do
// workspace CRUD on the workspaces view.
var sessionActionKeys = map[keys.KeyName]struct{}{
	keys.KeyPrompt: {}, keys.KeySubmit: {},
	keys.KeyCheckout: {}, keys.KeyResume: {}, keys.KeyRestart: {},
	keys.KeyFinish: {}, keys.KeyMoveUp: {}, keys.KeyMoveDown: {}, keys.KeyTab: {},
	keys.KeyShiftUp: {}, keys.KeyShiftDown: {},
}

func isSessionActionKey(name keys.KeyName) bool {
	_, ok := sessionActionKeys[name]
	return ok
}

// currentView returns the screen on top of the nav stack.
func (m *home) currentView() ui.View { return m.viewStack[len(m.viewStack)-1] }

// pushView adds a screen and re-lays-out for the new view. Any active "/" filter
// is cleared so it doesn't silently carry into the new screen.
func (m *home) pushView(v ui.View) {
	m.clearFilter()
	m.viewStack = append(m.viewStack, v)
}

// popView removes the top screen (no-op at the root), clearing any active filter.
func (m *home) popView() {
	if len(m.viewStack) > 1 {
		m.clearFilter()
		m.viewStack = m.viewStack[:len(m.viewStack)-1]
	}
}

// breadcrumb joins the stack's segments, e.g. "workspaces › sessions(backend)".
func (m *home) breadcrumb() string {
	parts := make([]string, len(m.viewStack))
	for i, v := range m.viewStack {
		parts[i] = v.Breadcrumb()
	}
	return strings.Join(parts, "/")
}

// refreshWorkspacesView rebuilds the workspaces table rows from the registry and
// live session counts.
func (m *home) refreshWorkspacesView() {
	reg := config.LoadWorkspaceRegistry()
	counts := map[string]int{}
	for _, inst := range m.list.GetInstances() {
		counts[inst.WorkspaceID]++
	}
	rows := make([]ui.WorkspaceRow, 0, len(reg.Workspaces))
	for _, w := range reg.Workspaces {
		rows = append(rows, ui.WorkspaceRow{
			ID:       w.ID,
			Name:     w.DisplayName,
			Repo:     w.RepoPath,
			Sessions: counts[w.ID],
			LastUsed: w.LastUsedAt,
			Active:   w.ID == m.workspaceID,
		})
	}
	m.workspacesView.SetRows(rows)
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	m.lastWindowSize = msg

	// Menu takes 10% of height; the header + content region share the other 90%.
	regionHeight := int(float32(msg.Height) * 0.9)
	menuHeight := msg.Height - regionHeight - 1      // minus 1 for error box
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1) // error box takes 1 row

	// Carve the header out of the top of the content region.
	m.header.SetSize(msg.Width)
	contentHeight := regionHeight - m.header.Height()
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Table-primary, drill-down: every view is full-width. The TabbedWindow is
	// sized to the full content region at ALL times — even when a table view is
	// showing — so its tmux preview dimensions never thrash as the user drills
	// in and out (see plan Risk #1).
	m.tabbedWindow.SetSize(msg.Width, contentHeight)
	m.list.SetSize(msg.Width, contentHeight)
	m.workspacesView.SetSize(msg.Width, contentHeight)
	m.sessionsView.SetSize(msg.Width, contentHeight)

	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(int(float32(msg.Width)*0.6), int(float32(msg.Height)*0.4))
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(msg.Width, menuHeight)
	m.cmdBar.SetSize(msg.Width, menuHeight)
	m.filterBar.SetSize(msg.Width, menuHeight)
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance()),
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case errMsg:
		return m, m.handleError(msg)
	case previewTickMsg:
		cmd := m.instanceChanged()
		return m, tea.Batch(
			cmd,
			func() tea.Msg {
				time.Sleep(100 * time.Millisecond)
				return previewTickMsg{}
			},
		)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case instanceStartDoneMsg:
		m.instanceStarting = false
		inst := msg.instance
		m.startingInstance = nil

		if msg.err != nil {
			// Start failed — remove the instance from the list and show the error.
			m.list.Kill()
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), m.handleError(msg.err))
		}

		// Save after successful start.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}

		if m.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = overlay.NewTextInputOverlay("Enter prompt", "")
			m.promptAfterName = false
		} else {
			m.showHelpScreen(helpStart(inst), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case metadataUpdateDoneMsg:
		for _, r := range msg.results {
			// Skip instances that were paused while metadata was being computed
			if r.instance.Status == session.Paused {
				continue
			}
			if !r.alive {
				// The agent process exited from inside the pane (or crashed):
				// the tmux session is gone. Flag it so the list shows a
				// distinct indicator and R/Enter offer a restart.
				r.instance.SetStatus(session.Exited)
			} else if r.updated {
				r.instance.SetStatus(session.Running)
			} else if r.hasPrompt {
				r.instance.TapEnter()
			} else {
				r.instance.SetStatus(session.Ready)
			}
			if r.diffStats != nil && r.diffStats.Error != nil {
				if !strings.Contains(r.diffStats.Error.Error(), "base commit SHA not set") {
					log.WarningLog.Printf("could not update diff stats: %v", r.diffStats.Error)
				}
				r.instance.SetDiffStats(nil)
			} else {
				r.instance.SetDiffStats(r.diffStats)
			}
		}
		return m, tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance())
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.Status == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.tabbedWindow.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.tabbedWindow.ScrollDown()
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		if m.textInputOverlay == nil {
			return m, nil
		}
		if msg.version != m.textInputOverlay.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.textInputOverlay != nil {
			m.textInputOverlay.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			repoPath := msg.instance.Path
			// Branch-collision (orphan worktree blocking) gets a dedicated
			// confirmation overlay so the user can clean it up in one keystroke
			// rather than having to read the errBox and run git commands by hand.
			var bce *git.BranchCollisionError
			if errors.As(msg.err, &bce) {
				// Capture replay options BEFORE Kill so we can recreate the
				// session after the orphan is gone. msg.selectedBranch is
				// the picker-selected branch (Shift+N flow); for the regular
				// N flow it's empty and the auto-generated name on i.Branch
				// was what collided — passing "" lets the retry regenerate.
				retry := pendingCreate{
					opts: session.InstanceOptions{
						Title:       msg.instance.Title,
						Path:        msg.instance.Path,
						Program:     msg.instance.Program,
						WorkspaceID: msg.instance.WorkspaceID,
						ProfileName: msg.instance.ProfileName,
						Branch:      msg.selectedBranch,
					},
					prompt:          msg.instance.Prompt,
					autoYes:         msg.instance.AutoYes,
					selectedBranch:  msg.selectedBranch,
					promptAfterName: msg.promptAfterName,
				}
				m.list.Kill()
				return m, tea.Batch(m.confirmCleanupOrphan(bce, repoPath, retry), m.instanceChanged())
			}
			m.list.Kill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newPromptOverlay()
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.ErrorLog.Printf("failed to send prompt: %v", err)
				}
				msg.instance.Prompt = ""
			}
			m.menu.SetState(ui.StateDefault)
			m.showHelpScreen(helpStart(msg.instance), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case replayCreateMsg:
		return m, m.replaySessionCreate(msg.retry)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateAddWorkspace || m.state == stateCommand || m.state == stateFilter {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter on stateNew, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNew {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	if m.state == stateCommand {
		return m.handleCommandState(msg)
	}

	if m.state == stateFilter {
		return m.handleFilterState(msg)
	}

	if m.state == stateNew {
		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Type {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}

			// If promptAfterName, show prompt+branch overlay before starting
			if m.promptAfterName {
				m.promptAfterName = false
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				m.textInputOverlay = m.newPromptOverlay()
				// Trigger initial branch search (no debounce, version 0)
				initialSearch := m.runBranchSearch("", m.textInputOverlay.BranchFilterVersion())
				return m, tea.Batch(tea.WindowSize(), initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)

			// Return a tea.Cmd that runs instance.Start in the background
			startCmd := func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyEsc:
			m.list.Kill()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Handle cancel via ctrl+c before delegating to the overlay
		if msg.String() == "ctrl+c" {
			return m, m.cancelPromptOverlay()
		}

		// Use the new TextInputOverlay component to handle all key events
		shouldClose, branchFilterChanged := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			if selected == nil {
				return m, nil
			}

			if m.textInputOverlay.IsCanceled() {
				return m, m.cancelPromptOverlay()
			}

			if m.textInputOverlay.IsSubmitted() {
				prompt := m.textInputOverlay.GetValue()
				selectedBranch := m.textInputOverlay.GetSelectedBranch()
				selectedProgram := m.textInputOverlay.GetSelectedProgram()

				if !selected.Started() {
					// Shift+N flow: instance not started yet — set branch, start, then send prompt
					if selectedBranch != "" {
						selected.SetSelectedBranch(selectedBranch)
					}
					if selectedProgram != "" {
						selected.Program = selectedProgram
					}
					selected.Prompt = prompt

					// Finalize into list and start
					selected.SetStatus(session.Loading)
					m.newInstanceFinalizer()
					m.textInputOverlay = nil
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)

					startCmd := func() tea.Msg {
						err := selected.Start(true)
						return instanceStartedMsg{
							instance:        selected,
							err:             err,
							promptAfterName: false,
							selectedBranch:  selectedBranch,
						}
					}

					return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
				}

				// Regular flow: instance already running, just send prompt
				if err := selected.SendPrompt(prompt); err != nil {
					return m, m.handleError(err)
				}
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					m.showHelpScreen(helpStart(selected), nil)
					return nil
				},
			)
		}

		// Schedule a debounced branch search if the filter changed
		if branchFilterChanged {
			filter := m.textInputOverlay.BranchFilter()
			version := m.textInputOverlay.BranchFilterVersion()
			return m, m.scheduleBranchSearch(filter, version)
		}

		return m, nil
	} else if m.state == stateAddWorkspace {
		if msg.String() == "ctrl+c" {
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.WindowSize()
		}
		// The underlying TextInputOverlay uses a multi-line textarea and routes
		// Enter to "add newline" unless focus is on a separate submit button.
		// For this single-line path entry we shortcut Enter directly to submit.
		if msg.Type == tea.KeyEnter {
			path := m.textInputOverlay.GetValue()
			m.textInputOverlay = nil
			m.state = stateDefault
			if err := m.addWorkspaceFromPath(path); err != nil {
				return m, tea.Batch(tea.WindowSize(), m.handleError(err))
			}
			return m, tea.WindowSize()
		}
		shouldClose, _ := m.textInputOverlay.HandleKeyPress(msg)
		if !shouldClose {
			return m, nil
		}
		// shouldClose only reaches here from Esc (cancel) since Enter is handled above.
		m.textInputOverlay = nil
		m.state = stateDefault
		return m, tea.WindowSize()
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			if cmd := m.pendingConfirmCmd; cmd != nil {
				m.pendingConfirmCmd = nil
				return m, cmd
			}
			return m, nil
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
		// If in preview tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInPreviewTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			// Use the selected instance from the list
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in terminal tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInTerminalTab() && m.tabbedWindow.IsTerminalInScrollMode() {
			m.tabbedWindow.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
		// A committed "/" filter is cleared first (before popping the stack), so
		// Esc peels off the filter, then drills back out.
		if m.filterText != "" {
			m.clearFilter()
			return m, m.instanceChanged()
		}
		// Otherwise, Esc pops the navigation stack (drill back out). At the root
		// view it is a no-op (it must not quit).
		if len(m.viewStack) > 1 {
			m.popView()
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
		}
	}

	// Enter the ":" command bar (k9s-style). Only reachable in stateDefault —
	// the modal states returned above.
	if msg.String() == ":" {
		m.state = stateCommand
		m.cmdBar.Reset()
		return m, nil
	}

	// Enter the "/" filter bar; the current filter (if any) is pre-loaded for
	// editing. Not available on the detail view (nothing to filter there).
	if msg.String() == "/" && m.currentView().Kind() != ui.ViewSessionDetail {
		m.state = stateFilter
		m.filterBar.Reset()
		if m.filterText != "" {
			m.filterBar.Insert(m.filterText)
		}
		return m, nil
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	// On the workspaces ("namespace") view, session-scoped actions don't apply —
	// the user must drill into a workspace first. Up/Down/Enter are handled in
	// their own cases below (they route by view kind). Workspace/help/quit keys
	// stay live.
	if m.currentView().Kind() == ui.ViewWorkspaces && isSessionActionKey(name) {
		return m, nil
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyPrompt:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}

		if cmd := m.requireWorkspace(); cmd != nil {
			return m, cmd
		}
		repoPath := m.sessionPath()

		// Start a background fetch so branches are up to date by the time the picker opens
		fetchCmd := func() tea.Msg {
			git.FetchBranches(repoPath)
			return nil
		}

		program, profileName := m.resolveWorkspaceProgram()
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:       "",
			Path:        repoPath,
			Program:     program,
			WorkspaceID: m.workspaceID,
			ProfileName: profileName,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, fetchCmd
	case keys.KeyNew:
		// Context-aware create: a new workspace on the workspaces view, a new
		// session everywhere else.
		if m.currentView().Kind() == ui.ViewWorkspaces {
			return m.openAddWorkspace()
		}
		return m.startNewSession()
	case keys.KeyUp:
		if m.currentView().Kind() == ui.ViewWorkspaces {
			m.workspacesView.MoveUp()
			return m, nil
		}
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		if m.currentView().Kind() == ui.ViewWorkspaces {
			m.workspacesView.MoveDown()
			return m, nil
		}
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.tabbedWindow.Toggle()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		// Context-aware delete: a workspace on the workspaces view, a session
		// everywhere else.
		if m.currentView().Kind() == ui.ViewWorkspaces {
			return m.confirmDeleteWorkspace()
		}
		return m.confirmKillSession()
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Show help screen before pausing
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			if err := selected.Pause(); err != nil {
				m.handleError(err)
			}
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
			m.instanceChanged()
		})
		return m, nil
	case keys.KeyMoveUp:
		if m.list.MoveUp() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyMoveDown:
		if m.list.MoveDown() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.WindowSize()
	case keys.KeyRestart:
		// Soft-reset: relaunch the agent for a Running session whose tmux
		// process exited, reusing the existing worktree. No-op (with an error
		// toast) if the session is still alive or is paused.
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if err := selected.Restart(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case keys.KeyFinish:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		return m, m.runFinishInteractive(selected)
	case keys.KeyAddWorkspace:
		return m.openAddWorkspace()
	case keys.KeyEnter:
		// Drill-down: Enter on the workspaces view opens that workspace's
		// sessions; Enter on the sessions view opens the selected session's
		// detail; Enter inside the detail view attaches (handled below).
		switch m.currentView().Kind() {
		case ui.ViewWorkspaces:
			id := m.workspacesView.SelectedWorkspaceID()
			if id == "" {
				return m, nil
			}
			m.applyWorkspaceFocus(id)
			m.sessionsView.SetScopeLabel(m.labelForFilter(id))
			m.pushView(m.sessionsView)
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
		case ui.ViewSessions:
			if m.list.GetSelectedInstance() == nil {
				return m, nil
			}
			m.sessionDetailView.SetScopeLabel(m.labelForFilter(m.list.GetSelectedInstance().WorkspaceID))
			m.pushView(m.sessionDetailView)
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
		}

		// ViewSessionDetail: attach to the selected session.
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || selected.Status == session.Loading {
			return m, nil
		}
		// Terminal tab: attach to terminal session
		if m.tabbedWindow.IsInTerminalTab() {
			if !selected.TmuxAlive() {
				return m, nil
			}
			m.showHelpScreen(helpTypeInstanceAttach{}, func() {
				ch, err := m.tabbedWindow.AttachTerminal()
				if err != nil {
					m.handleError(err)
					return
				}
				<-ch
				m.state = stateDefault
			})
			return m, nil
		}
		// If the agent exited from inside the pane, the tmux session is dead.
		// Soft-reset it in place first so Enter still attaches — the worktree
		// and branch are left intact (unlike a full Resume).
		if !selected.TmuxAlive() {
			if err := selected.Restart(); err != nil {
				return m, m.handleError(err)
			}
			m.instanceChanged()
		}
		// Show help screen before attaching
		m.showHelpScreen(helpTypeInstanceAttach{}, func() {
			ch, err := m.list.Attach()
			if err != nil {
				m.handleError(err)
				return
			}
			<-ch
			m.state = stateDefault
			m.instanceChanged()
		})
		return m, nil
	default:
		return m, nil
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type instanceChangedMsg struct{}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		currentDir, _ := os.Getwd()
		branches, err := git.SearchBranches(currentDir, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	alive     bool
	diffStats *git.DiffStats
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// instanceStartDoneMsg is sent when the background instance start completes.
type instanceStartDoneMsg struct {
	instance *session.Instance
	err      error
}

// runInstanceStartCmd returns a Cmd that performs the expensive instance.Start(true)
// in a background goroutine so the main event loop stays responsive.
func runInstanceStartCmd(instance *session.Instance) tea.Cmd {
	return func() tea.Msg {
		err := instance.Start(true)
		return instanceStartDoneMsg{instance: instance, err: err}
	}
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(active []*session.Instance, selected *session.Instance) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		if len(active) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(active))
		var wg sync.WaitGroup
		for idx, inst := range active {
			wg.Add(1)
			go func(i int, instance *session.Instance) {
				defer wg.Done()
				r := &results[i]
				r.instance = instance
				// Auto-accept the per-worktree trust prompt (claude's "Do you
				// trust the files in this folder?", aider/gemini equivalents).
				// Without this the agent sits on the trust screen and looks
				// like it never launched. Safe to call every tick — it's a
				// pane scrape + one keystroke that no-ops once the prompt is
				// gone.
				instance.CheckAndHandleTrustPrompt()
				r.updated, r.hasPrompt = instance.HasUpdated()
				r.alive = instance.TmuxAlive()
				if instance == selected {
					r.diffStats = instance.ComputeDiff()
				} else {
					r.diffStats = instance.ComputeDiffNumstat()
				}
			}(idx, inst)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
// runFinishInteractive suspends the TUI and execs `cs finish --interactive`
// for the selected session, with CS_* env vars populated from the instance
// so the subprocess resolves the right journal. Quitting the editor without
// saving aborts harmlessly; a save+quit either records a finish event or
// preserves the temp file for the operator to retry.
func (m *home) runFinishInteractive(inst *session.Instance) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return m.handleError(fmt.Errorf("locate cs binary: %w", err))
	}
	cmd := exec.Command(exe, "finish", "--interactive")
	cmd.Env = append(os.Environ(), inst.EnvForExternalProcess()...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return errMsg(err)
		}
		return nil
	})
}

// errMsg is a tea.Msg wrapper for an error returned from a suspended
// subprocess, routed through handleError to populate the err box.
type errMsg error

func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up unstarted instances.
func (m *home) cancelPromptOverlay() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected != nil && !selected.Started() {
		m.list.Kill()
	}
	m.textInputOverlay = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Execute the action if it exists
		if action != nil {
			_ = action()
		}
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
	}

	return nil
}

// pendingCreate carries everything needed to replay a failed session create
// after an orphan worktree has been cleaned up. Held by confirmCleanupOrphan's
// closure so the retry uses the exact same title/path/program/branch/prompt
// the user originally asked for — no need to re-prompt.
type pendingCreate struct {
	opts            session.InstanceOptions
	prompt          string
	autoYes         bool
	selectedBranch  string
	promptAfterName bool
}

// confirmCleanupOrphan opens an overlay offering to nuke the worktree+branch
// referenced by a BranchCollisionError. On confirm, removes the orphan and
// immediately re-attempts the failed session create from `retry`, so the user
// goes from "session-create blocked" to "session running" in one keystroke
// instead of being dumped back at the menu. On cancel, surfaces the original
// collision error so the user still has the path + manual-recovery command.
func (m *home) confirmCleanupOrphan(bce *git.BranchCollisionError, repoPath string, retry pendingCreate) tea.Cmd {
	message := fmt.Sprintf("Branch %q is held by an orphan worktree at:\n%s\n\nRemove worktree + branch and recreate the session?",
		bce.Branch, bce.WorktreePath)

	m.state = stateConfirm
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	m.confirmationOverlay.SetWidth(70)

	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		branch, path := bce.Branch, bce.WorktreePath
		m.pendingConfirmCmd = func() tea.Msg {
			if err := git.RemoveOrphanWorktree(repoPath, path, branch); err != nil {
				return fmt.Errorf("orphan cleanup failed: %w", err)
			}
			// Cleanup OK — hand the retry off to Update on the UI thread so
			// AddInstance / list mutations don't race with rendering.
			return replayCreateMsg{retry: retry}
		}
	}
	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
		// Re-surface the collision error so the user has the path + manual
		// recovery command in front of them after dismissing the overlay.
		m.pendingConfirmCmd = func() tea.Msg { return error(bce) }
	}

	return nil
}

// replayCreateMsg is delivered to Update on the UI thread after an orphan
// worktree has been cleaned up, so the failed session-create can be replayed
// without racing with rendering.
type replayCreateMsg struct {
	retry pendingCreate
}

// replaySessionCreate rebuilds the session that failed with a BranchCollisionError
// and kicks off Start() in the background. Must be called from Update (mutates m).
func (m *home) replaySessionCreate(r pendingCreate) tea.Cmd {
	instance, err := session.NewInstance(r.opts)
	if err != nil {
		return m.handleError(fmt.Errorf("retry session: %w", err))
	}
	if r.autoYes {
		instance.AutoYes = true
	}
	if r.prompt != "" {
		instance.Prompt = r.prompt
	}
	finalizer := m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	instance.SetStatus(session.Loading)
	finalizer()
	selectedBranch := r.selectedBranch
	promptAfterName := r.promptAfterName
	startCmd := func() tea.Msg {
		startErr := instance.Start(true)
		return instanceStartedMsg{
			instance:        instance,
			err:             startErr,
			promptAfterName: promptAfterName,
			selectedBranch:  selectedBranch,
		}
	}
	return tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
}

// startNewSession creates a fresh (unnamed) instance and enters name-entry
// mode. Shared by the `n` key and the `:new` command.
func (m *home) startNewSession() (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	if cmd := m.requireWorkspace(); cmd != nil {
		return m, cmd
	}
	program, profileName := m.resolveWorkspaceProgram()
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:       "",
		Path:        m.sessionPath(),
		Program:     program,
		WorkspaceID: m.workspaceID,
		ProfileName: profileName,
	})
	if err != nil {
		return m, m.handleError(err)
	}

	m.newInstanceFinalizer = m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	return m, nil
}

// handleCommandState routes keypresses while the ":" command bar is open. It
// mirrors the rune-capture pattern used by stateNew: runes/space accumulate,
// backspace trims, Enter dispatches, Esc/ctrl+c cancel.
func (m *home) handleCommandState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		input := strings.TrimSpace(m.cmdBar.Value())
		if input == "" {
			m.state = stateDefault
			m.cmdBar.Reset()
			return m, nil
		}
		return m.executeCommand(input)
	case tea.KeyEsc:
		m.state = stateDefault
		m.cmdBar.Reset()
		return m, nil
	case tea.KeyRunes:
		m.cmdBar.Insert(string(msg.Runes))
		return m, nil
	case tea.KeySpace:
		m.cmdBar.Insert(" ")
		return m, nil
	case tea.KeyBackspace:
		m.cmdBar.Backspace()
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		m.state = stateDefault
		m.cmdBar.Reset()
		return m, nil
	}
	return m, nil
}

// executeCommand dispatches a parsed command-bar verb. On success it returns to
// stateDefault and clears the bar; on a bad command it sets an error on the bar
// and stays in stateCommand so the user can edit and retry.
func (m *home) executeCommand(input string) (tea.Model, tea.Cmd) {
	verb, args, _ := ui.ParseCommand(input)
	rawVerb := strings.Fields(input)[0] // case preserved for key matching

	fail := func(msg string) (tea.Model, tea.Cmd) {
		m.cmdBar.SetError(msg) // stays in stateCommand
		return m, nil
	}
	done := func(model tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.state = stateDefault
		m.cmdBar.Reset()
		m.clearFilter() // a command-driven view switch starts unfiltered
		return model, cmd
	}

	// Any keybinding is invokable as ":<key>" — e.g. :c :p :r :R :F :W :A. Match
	// the raw token case-sensitively (so :R restart ≠ :r resume) before the
	// lowercased word commands below.
	if _, isKey := keys.GlobalKeyStringsMap[rawVerb]; isKey {
		m.state = stateDefault
		m.cmdBar.Reset()
		return m.dispatchSyntheticKey(rawVerb)
	}

	switch verb {
	case "workspaces":
		// Jump to the workspaces ("namespace") list as the root.
		m.viewStack = []ui.View{m.workspacesView}
		return done(m, tea.WindowSize())
	case "sessions":
		// Unscoped sessions list as the root.
		m.applyWorkspaceFocus("")
		m.sessionsView.SetScopeLabel("All")
		m.viewStack = []ui.View{m.sessionsView}
		return done(m, tea.Batch(tea.WindowSize(), m.instanceChanged()))
	case "ws":
		if len(args) == 0 {
			return fail("usage: ws <name>")
		}
		name := strings.Join(args, " ")
		reg := config.LoadWorkspaceRegistry()
		ws := reg.FindByName(name)
		if ws == nil {
			return fail(fmt.Sprintf("no workspace: %s", name))
		}
		m.applyWorkspaceFocus(ws.ID)
		m.sessionsView.SetScopeLabel(ws.DisplayName)
		// Stack workspaces → sessions(scoped) so Esc returns to the ws list.
		m.viewStack = []ui.View{m.workspacesView, m.sessionsView}
		return done(m, tea.Batch(tea.WindowSize(), m.instanceChanged()))
	case "new":
		m.state = stateDefault
		m.cmdBar.Reset()
		// Context-aware: a workspace on the workspaces view, else a session.
		if m.currentView().Kind() == ui.ViewWorkspaces {
			return m.openAddWorkspace()
		}
		// startNewSession sets stateNew on success (or returns an error with
		// state reset to default above).
		return m.startNewSession()
	case "delete":
		m.state = stateDefault
		m.cmdBar.Reset()
		// Context-aware delete of the current selection.
		if m.currentView().Kind() == ui.ViewWorkspaces {
			return m.confirmDeleteWorkspace()
		}
		return m.confirmKillSession()
	case "quit":
		return m.handleQuit()
	case "help":
		m.state = stateDefault
		m.cmdBar.Reset()
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	default:
		// Readable word aliases for the action keys: :checkout :push :resume ...
		if ks, ok := actionWordToKey[verb]; ok {
			m.state = stateDefault
			m.cmdBar.Reset()
			return m.dispatchSyntheticKey(ks)
		}
		return fail(fmt.Sprintf("unknown command: %s", verb))
	}
}

// actionWordToKey maps readable command words to the keybinding they invoke, so
// ":checkout" works as well as ":c". Word commands handled explicitly above
// (workspaces/sessions/ws/new/delete/quit/help) are intentionally absent.
var actionWordToKey = map[string]string{
	"checkout":     "c",
	"push":         "p",
	"resume":       "r",
	"restart":      "R",
	"finish":       "F",
	"attach":       "enter",
	"open":         "enter",
	"prompt":       "N",
	"add":          "A",
	"addworkspace": "A",
}

// dispatchSyntheticKey re-feeds a keybinding through the normal key handler, so
// the command bar reuses every existing key action. State is already reset to
// default by the caller.
func (m *home) dispatchSyntheticKey(keyStr string) (tea.Model, tea.Cmd) {
	return m.handleKeyPress(synthKeyMsg(keyStr))
}

// synthKeyMsg builds the tea.KeyMsg for a keybinding string. Named keys map to
// their KeyType (so msg.String() round-trips); everything else is sent as runes.
func synthKeyMsg(keyStr string) tea.KeyMsg {
	switch keyStr {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyStr)}
	}
}

// openAddWorkspace opens the "add workspace" path-entry overlay. Shared by the
// A key and the context-aware n key (on the workspaces view) and :new.
func (m *home) openAddWorkspace() (tea.Model, tea.Cmd) {
	m.textInputOverlay = overlay.NewTextInputOverlay(
		"Add workspace (enter a path; new dirs are git-init'd automatically)",
		"",
	)
	m.state = stateAddWorkspace
	// tea.WindowSize triggers updateHandleWindowSizeEvent which calls
	// m.textInputOverlay.SetSize. Without this, the embedded textarea has
	// width 0 → renders one char per line.
	return m, tea.WindowSize()
}

// confirmKillSession kills the selected session after a confirmation. Shared by
// the D key and the :delete command.
func (m *home) confirmKillSession() (tea.Model, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Status == session.Loading {
		return m, nil
	}
	killAction := func() tea.Msg {
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return err
		}
		checkedOut, err := worktree.IsBranchCheckedOut()
		if err != nil {
			return err
		}
		if checkedOut {
			return fmt.Errorf("instance %s is currently checked out", selected.Title)
		}
		m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
		if err := m.storage.DeleteInstance(selected.Title); err != nil {
			return err
		}
		m.list.Kill()
		return instanceChangedMsg{}
	}
	return m, m.confirmAction(fmt.Sprintf("[!] Kill session '%s'?", selected.Title), killAction)
}

// sessionCountFor returns how many sessions currently belong to a workspace.
func (m *home) sessionCountFor(wsID string) int {
	n := 0
	for _, inst := range m.list.GetInstances() {
		if inst.WorkspaceID == wsID {
			n++
		}
	}
	return n
}

// confirmDeleteWorkspace deletes the highlighted workspace after a confirmation,
// refusing if it still has sessions (they'd be orphaned — kill them first).
func (m *home) confirmDeleteWorkspace() (tea.Model, tea.Cmd) {
	id := m.workspacesView.SelectedWorkspaceID()
	if id == "" {
		return m, nil
	}
	reg := config.LoadWorkspaceRegistry()
	ws := reg.Get(id)
	if ws == nil {
		return m, nil
	}
	if n := m.sessionCountFor(id); n > 0 {
		return m, m.handleError(fmt.Errorf(
			"can't delete workspace %q: %d active session(s) — kill them first", ws.DisplayName, n))
	}
	name := ws.DisplayName
	action := func() tea.Msg {
		if err := config.LoadWorkspaceRegistry().Remove(id); err != nil {
			return err
		}
		// If we just deleted the active workspace, drop the focus.
		if m.workspaceID == id {
			m.workspaceID = ""
			m.list.SetActiveWorkspace("")
			m.list.SetActiveWorkspaceID("")
		}
		return instanceChangedMsg{}
	}
	return m, m.confirmAction(fmt.Sprintf("[!] Delete workspace '%s'?", name), action)
}

// handleFilterState routes keypresses while the "/" filter bar is open. The
// filter applies live as the user types; Enter commits it (exits input but keeps
// filtering); Esc/ctrl+c clears it.
func (m *home) handleFilterState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// Commit: keep the filter, return to navigation.
		m.state = stateDefault
		return m, m.instanceChanged()
	case tea.KeyEsc:
		m.clearFilter()
		m.state = stateDefault
		return m, m.instanceChanged()
	case tea.KeyRunes:
		m.filterBar.Insert(string(msg.Runes))
	case tea.KeySpace:
		m.filterBar.Insert(" ")
	case tea.KeyBackspace:
		m.filterBar.Backspace()
	default:
		if msg.String() == "ctrl+c" {
			m.clearFilter()
			m.state = stateDefault
			return m, m.instanceChanged()
		}
		return m, nil
	}
	// Apply live after any edit.
	m.filterText = m.filterBar.Value()
	m.applyFilter()
	return m, m.instanceChanged()
}

// applyFilter pushes the current filterText into whichever table is active.
func (m *home) applyFilter() {
	switch m.currentView().Kind() {
	case ui.ViewWorkspaces:
		m.workspacesView.SetFilter(m.filterText)
	case ui.ViewSessions:
		m.list.SetTextFilter(m.filterText)
	}
}

// clearFilter removes the active filter from both tables.
func (m *home) clearFilter() {
	m.filterText = ""
	m.filterBar.Reset()
	m.list.SetTextFilter("")
	m.workspacesView.SetFilter("")
}

// commandOrMenu renders the command/filter bar in place of the menu while
// capturing input, so the bottom region keeps a stable height.
func (m *home) commandOrMenu() string {
	switch m.state {
	case stateCommand:
		return m.cmdBar.String()
	case stateFilter:
		return m.filterBar.String()
	}
	return m.menu.String()
}

// updateHeader refreshes the top banner's context data before each render.
func (m *home) updateHeader() {
	m.menu.SetWorkspacesMode(m.currentView().Kind() == ui.ViewWorkspaces)
	reg := config.LoadWorkspaceRegistry()
	m.header.Update(
		config.Version,
		m.labelForFilter(m.workspaceID),
		m.list.NumInstances(),
		len(reg.Workspaces),
		m.breadcrumb(),
		m.filterText,
	)
}

func (m *home) View() string {
	m.updateHeader()
	// Refresh the workspaces table data when it's the active screen.
	if m.currentView().Kind() == ui.ViewWorkspaces {
		m.refreshWorkspacesView()
	}

	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		m.header.String(),
		m.currentView().String(),
		m.commandOrMenu(),
		m.errBox.String(),
	)

	if m.state == statePrompt || m.state == stateAddWorkspace {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true)
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true)
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true)
	}

	return mainView
}
