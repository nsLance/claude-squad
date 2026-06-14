package app

import (
	"testing"

	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/ui"

	"github.com/stretchr/testify/require"
)

// addWorkspaceWithSession registers a workspace in the (temp) registry and
// optionally attaches a session to it in the list.
func registerWorkspace(t *testing.T, name, repo string) *config.Workspace {
	t.Helper()
	reg := config.LoadWorkspaceRegistry()
	ws, err := reg.EnsureWorkspace(repo, "")
	require.NoError(t, err)
	ws.DisplayName = name
	require.NoError(t, reg.Upsert(*ws))
	return reg.Get(ws.ID)
}

func TestConfirmDeleteWorkspace_BlockedByActiveSessions(t *testing.T) {
	h := newNavTestHome(t) // sets an isolated CLAUDE_SQUAD_HOME
	ws := registerWorkspace(t, "backend", t.TempDir())

	// Attach a session to that workspace.
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "live", Path: ".", Program: "true", WorkspaceID: ws.ID,
	})
	require.NoError(t, err)
	h.list.AddInstance(inst)

	require.Equal(t, 1, h.sessionCountFor(ws.ID))

	// Point the workspaces view at our workspace and attempt delete.
	h.refreshWorkspacesView()
	h.workspacesView.SetRows([]ui.WorkspaceRow{{ID: ws.ID, Name: ws.DisplayName}})

	_, cmd := h.confirmDeleteWorkspace()
	// Guard fires: no confirmation modal, an error cmd is returned instead.
	require.NotEqual(t, stateConfirm, h.state, "delete must be refused while sessions exist")
	require.NotNil(t, cmd)

	// The workspace is still registered.
	require.NotNil(t, config.LoadWorkspaceRegistry().Get(ws.ID))
}

func TestConfirmDeleteWorkspace_AllowedWhenEmpty(t *testing.T) {
	h := newNavTestHome(t)
	ws := registerWorkspace(t, "empty-ws", t.TempDir())
	require.Equal(t, 0, h.sessionCountFor(ws.ID))

	h.workspacesView.SetRows([]ui.WorkspaceRow{{ID: ws.ID, Name: ws.DisplayName}})

	_, _ = h.confirmDeleteWorkspace()
	require.Equal(t, stateConfirm, h.state, "empty workspace delete shows a confirmation")
	require.NotNil(t, h.confirmationOverlay)

	// Simulate the user confirming.
	h.confirmationOverlay.OnConfirm()
	require.Nil(t, config.LoadWorkspaceRegistry().Get(ws.ID), "workspace removed after confirm")
}
