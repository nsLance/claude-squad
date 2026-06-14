package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestFilter_LiveTypingAndCommit(t *testing.T) {
	h := newNavTestHome(t)
	// Drill into the sessions view (filter applies to the session list there).
	h.pushView(h.sessionsView)

	h.state = stateFilter
	h.handleFilterState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("au")})
	require.Equal(t, "au", h.filterText)
	require.Equal(t, "au", h.list.GetTextFilter(), "filter applies live to the session list")

	// Enter commits: leaves input mode but keeps the filter active.
	h.handleFilterState(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "au", h.filterText)
	require.Equal(t, "au", h.list.GetTextFilter())
}

func TestFilter_EscClears(t *testing.T) {
	h := newNavTestHome(t)
	h.pushView(h.sessionsView)
	h.state = stateFilter
	h.handleFilterState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	require.Equal(t, "x", h.filterText)

	h.handleFilterState(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "", h.filterText)
	require.Equal(t, "", h.list.GetTextFilter(), "Esc clears the live filter")
}

func TestFilter_ClearedOnNavChange(t *testing.T) {
	h := newNavTestHome(t)
	h.pushView(h.sessionsView)
	h.filterText = "foo"
	h.list.SetTextFilter("foo")

	h.popView() // back to workspaces
	require.Equal(t, "", h.filterText, "navigating clears the filter")
	require.Equal(t, "", h.list.GetTextFilter())
}

func TestFilter_AppliesToWorkspacesView(t *testing.T) {
	h := newNavTestHome(t)
	require.Equal(t, "workspaces", h.currentView().Breadcrumb())
	h.state = stateFilter
	h.handleFilterState(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("be")})
	require.Equal(t, "be", h.filterText)
	// No panic + filter recorded; the workspaces table applies it internally.
	h.handleFilterState(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, "", h.filterText)
}
