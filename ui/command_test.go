package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCommand_Aliases(t *testing.T) {
	cases := map[string]string{
		"workspaces": "workspaces",
		"ns":         "workspaces",
		"sessions":   "sessions",
		"s":          "sessions",
		"sess":       "sessions",
		"all":        "sessions",
		"ws":         "ws",
		"workspace":  "ws",
		"new":        "new",
		"n":          "new",
		"quit":       "quit",
		"q":          "quit",
		"exit":       "quit",
		"help":       "help",
		"?":          "help",
	}
	for in, want := range cases {
		verb, _, ok := ParseCommand(in)
		require.True(t, ok, "ParseCommand(%q) should be ok", in)
		require.Equal(t, want, verb, "ParseCommand(%q)", in)
	}
}

func TestParseCommand_ArgsAndCase(t *testing.T) {
	verb, args, ok := ParseCommand("  WS  my workspace  ")
	require.True(t, ok)
	require.Equal(t, "ws", verb)
	require.Equal(t, []string{"my", "workspace"}, args)
}

func TestParseCommand_Empty(t *testing.T) {
	_, _, ok := ParseCommand("   ")
	require.False(t, ok)
}

func TestParseCommand_UnknownPassesThrough(t *testing.T) {
	verb, _, ok := ParseCommand("bogus")
	require.True(t, ok)
	require.Equal(t, "bogus", verb)
}

func TestCommandBar_EditingAndError(t *testing.T) {
	c := NewCommandBar()
	c.SetSize(80, 1)
	c.Insert("ws ")
	c.Insert("backend")
	require.Equal(t, "ws backend", c.Value())
	c.Backspace()
	require.Equal(t, "ws backen", c.Value())

	c.SetError("no workspace: x")
	require.Contains(t, c.String(), "no workspace: x")

	// Editing clears the error.
	c.Insert("d")
	require.NotContains(t, c.String(), "no workspace")

	c.Reset()
	require.Equal(t, "", c.Value())
}
