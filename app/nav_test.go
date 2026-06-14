package app

import (
	"context"
	"testing"

	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/ui"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/require"
)

func newNavTestHome(t *testing.T) *home {
	t.Helper()
	t.Setenv(config.ConfigHomeEnvVar, t.TempDir())
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
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
	h.sessionsView.SetScopeLabel("All")
	h.sessionDetailView = ui.NewSessionDetailView(h.tabbedWindow, func() string { return "demo" })
	h.viewStack = []ui.View{h.workspacesView}
	return h
}

func TestNav_PushPop(t *testing.T) {
	h := newNavTestHome(t)
	require.Equal(t, ui.ViewWorkspaces, h.currentView().Kind())

	h.pushView(h.sessionsView)
	require.Equal(t, ui.ViewSessions, h.currentView().Kind())
	require.Len(t, h.viewStack, 2)

	h.pushView(h.sessionDetailView)
	require.Equal(t, ui.ViewSessionDetail, h.currentView().Kind())

	h.popView()
	require.Equal(t, ui.ViewSessions, h.currentView().Kind())
	h.popView()
	require.Equal(t, ui.ViewWorkspaces, h.currentView().Kind())

	// Root pop is a no-op.
	h.popView()
	require.Len(t, h.viewStack, 1)
	require.Equal(t, ui.ViewWorkspaces, h.currentView().Kind())
}

func TestNav_Breadcrumb(t *testing.T) {
	h := newNavTestHome(t)
	require.Equal(t, "workspaces", h.breadcrumb())

	h.sessionsView.SetScopeLabel("backend")
	h.pushView(h.sessionsView)
	require.Equal(t, "workspaces/sessions(backend)", h.breadcrumb())

	h.pushView(h.sessionDetailView)
	require.Equal(t, "workspaces/sessions(backend)/demo", h.breadcrumb())
}

func TestNav_SessionActionGuard(t *testing.T) {
	h := newNavTestHome(t)
	// On the workspaces view, session actions are gated out.
	require.Equal(t, ui.ViewWorkspaces, h.currentView().Kind())
	require.True(t, isSessionActionKey(keys.KeyKill))
	require.False(t, isSessionActionKey(keys.KeyHelp))

	// On the sessions view they are allowed (the guard only fires on workspaces).
	h.pushView(h.sessionsView)
	require.Equal(t, ui.ViewSessions, h.currentView().Kind())
}
