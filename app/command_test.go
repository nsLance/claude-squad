package app

import (
	"context"
	"testing"

	"claude-squad/config"
	"claude-squad/ui"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// newCommandTestHome builds a minimal home wired for command-bar tests, with an
// isolated config home so workspace lookups don't touch the real registry.
func newCommandTestHome(t *testing.T) *home {
	t.Helper()
	t.Setenv(config.ConfigHomeEnvVar, t.TempDir())

	h := &home{
		ctx:          context.Background(),
		state:        stateCommand,
		appConfig:    config.DefaultConfig(),
		spinner:      spinner.New(),
		cmdBar:       ui.NewCommandBar(),
		filterBar:    ui.NewBarWithPrompt("/"),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
	}
	h.list = ui.NewList(&h.spinner, false)
	h.workspacesView = ui.NewWorkspacesView()
	h.sessionsView = ui.NewSessionsView(h.list)
	h.sessionDetailView = ui.NewSessionDetailView(h.tabbedWindow, func() string { return "" })
	h.viewStack = []ui.View{h.sessionsView}
	return h
}

func TestCommandBar_RuneCaptureAndCancel(t *testing.T) {
	h := newCommandTestHome(t)

	h.handleCommandState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	h.handleCommandState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	h.handleCommandState(tea.KeyMsg{Type: tea.KeySpace})
	h.handleCommandState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	require.Equal(t, "ws x", h.cmdBar.Value())

	h.handleCommandState(tea.KeyMsg{Type: tea.KeyBackspace})
	require.Equal(t, "ws ", h.cmdBar.Value())

	// Esc cancels back to default and clears the bar.
	h.handleCommandState(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "", h.cmdBar.Value())
}

func TestExecuteCommand_UnknownStaysInCommandWithError(t *testing.T) {
	h := newCommandTestHome(t)
	h.executeCommand("bogus")
	require.Equal(t, stateCommand, h.state, "bad command keeps the bar open")
	require.Contains(t, h.cmdBar.String(), "unknown command")
}

func TestExecuteCommand_WsUnknownNameErrors(t *testing.T) {
	h := newCommandTestHome(t)
	h.executeCommand("ws does-not-exist")
	require.Equal(t, stateCommand, h.state)
	require.Contains(t, h.cmdBar.String(), "no workspace")
}

func TestExecuteCommand_WsMissingArg(t *testing.T) {
	h := newCommandTestHome(t)
	h.executeCommand("ws")
	require.Equal(t, stateCommand, h.state)
	require.Contains(t, h.cmdBar.String(), "usage: ws")
}

func TestExecuteCommand_SessionsClearsScope(t *testing.T) {
	h := newCommandTestHome(t)
	h.list.SetViewFilter("some-id")

	h.executeCommand("sessions")
	require.Equal(t, stateDefault, h.state, "successful command returns to default")
	require.Equal(t, "", h.cmdBar.Value())
	require.Equal(t, "", h.list.GetViewFilter(), "sessions clears the workspace scope")
}

func TestEnterCommandMode(t *testing.T) {
	h := newCommandTestHome(t)
	h.state = stateDefault

	// Enter on empty input returns to default without error.
	h.state = stateCommand
	h.handleCommandState(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateDefault, h.state)
}
