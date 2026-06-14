package keys

import (
	"github.com/charmbracelet/bubbles/key"
)

type KeyName int

const (
	KeyUp KeyName = iota
	KeyDown
	KeyEnter
	KeyNew
	KeyKill
	KeyQuit
	KeyReview
	KeyPush
	KeySubmit

	KeyTab        // Tab is a special keybinding for switching between panes.
	KeySubmitName // SubmitName is a special keybinding for submitting the name of a new instance.

	KeyCheckout
	KeyResume
	// KeyRestart soft-resets a Running session whose tmux process exited: it
	// relaunches the agent in the existing worktree (worktree/branch untouched).
	KeyRestart
	KeyHelp // Key for showing help screen

	// Diff keybindings
	KeyShiftUp
	KeyShiftDown

	// KeyAddWorkspace opens an overlay for adding a new workspace by path.
	KeyAddWorkspace
	// KeyFinish suspends the TUI to run `cs finish --interactive` for the
	// selected session — opens $EDITOR with a task-record template, then
	// writes a finish event on save+quit. Requires confirm via the editor's
	// own save action; quitting without saving aborts.
	KeyFinish

	// Reorder keybindings
	KeyMoveUp
	KeyMoveDown

	// KeyRecycle "rebuilds" the selected session: it relaunches the agent with
	// its continue command in the same worktree, first asking a running agent to
	// quit gracefully. Works in any non-loading state.
	KeyRecycle
)

// GlobalKeyStringsMap is a global, immutable map string to keybinding.
var GlobalKeyStringsMap = map[string]KeyName{
	"up":         KeyUp,
	"k":          KeyUp,
	"down":       KeyDown,
	"j":          KeyDown,
	"shift+up":   KeyShiftUp,
	"shift+down": KeyShiftDown,
	"J":          KeyMoveDown,
	"K":          KeyMoveUp,
	"enter":      KeyEnter,
	"o":          KeyEnter,
	"n":          KeyNew,
	"D":          KeyKill,
	"q":          KeyQuit,
	"tab":        KeyTab,
	"c":          KeyCheckout,
	"r":          KeyResume,
	"R":          KeyRestart,
	"p":          KeySubmit,
	"?":          KeyHelp,
	"A":          KeyAddWorkspace,
	"F":          KeyFinish,
	"X":          KeyRecycle,
}

// GlobalkeyBindings is a global, immutable map of KeyName tot keybinding.
var GlobalkeyBindings = map[KeyName]key.Binding{
	KeyUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	KeyDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	KeyShiftUp: key.NewBinding(
		key.WithKeys("shift+up"),
		key.WithHelp("shift+↑", "scroll"),
	),
	KeyShiftDown: key.NewBinding(
		key.WithKeys("shift+down"),
		key.WithHelp("shift+↓", "scroll"),
	),
	KeyEnter: key.NewBinding(
		key.WithKeys("enter", "o"),
		key.WithHelp("↵/o", "open"),
	),
	KeyNew: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	KeyKill: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "delete"),
	),
	KeyHelp: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	KeyQuit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	KeySubmit: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "push branch"),
	),
	KeyCheckout: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "checkout"),
	),
	KeyTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch tab"),
	),
	KeyResume: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "resume"),
	),
	KeyRestart: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "restart"),
	),
	KeyAddWorkspace: key.NewBinding(
		key.WithKeys("A"),
		key.WithHelp("A", "add workspace"),
	),
	KeyFinish: key.NewBinding(
		key.WithKeys("F"),
		key.WithHelp("F", "finish (audit closeout)"),
	),

	KeyMoveUp: key.NewBinding(
		key.WithKeys("K"),
		key.WithHelp("K", "move up"),
	),
	KeyMoveDown: key.NewBinding(
		key.WithKeys("J"),
		key.WithHelp("J", "move down"),
	),

	KeyRecycle: key.NewBinding(
		key.WithKeys("X"),
		key.WithHelp("X", "rebuild"),
	),

	// -- Special keybindings --

	KeySubmitName: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit name"),
	),
}
