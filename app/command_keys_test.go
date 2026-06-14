package app

import (
	"testing"

	"claude-squad/keys"

	"github.com/stretchr/testify/require"
)

// TestKeyVerbsAreCaseSensitive guards the :R (restart) vs :r (resume) distinction
// that lowercasing the verb would have destroyed.
func TestKeyVerbsAreCaseSensitive(t *testing.T) {
	require.Equal(t, keys.KeyRestart, keys.GlobalKeyStringsMap["R"])
	require.Equal(t, keys.KeyResume, keys.GlobalKeyStringsMap["r"])
	require.Equal(t, keys.KeyPrompt, keys.GlobalKeyStringsMap["N"])
	require.Equal(t, keys.KeyNew, keys.GlobalKeyStringsMap["n"])
}

func TestActionWordAliasesMapToKeys(t *testing.T) {
	cases := map[string]string{
		"checkout": "c",
		"push":     "p",
		"resume":   "r",
		"restart":  "R",
		"finish":   "F",
		"switch":   "W",
		"add":      "A",
	}
	for word, keyStr := range cases {
		require.Equal(t, keyStr, actionWordToKey[word], "word %q", word)
		// And every target must be a real binding.
		_, ok := keys.GlobalKeyStringsMap[keyStr]
		require.True(t, ok, "actionWordToKey[%q]=%q must be a real key", word, keyStr)
	}
}

func TestExecuteCommand_KeyVerbsDispatchAndExit(t *testing.T) {
	h := newNavTestHome(t)
	h.pushView(h.sessionsView) // session actions are live on the sessions view

	for _, in := range []string{"c", "p", "r", "R", "F", "checkout", "push", "restart"} {
		h.state = stateCommand
		h.cmdBar.Reset()
		h.executeCommand(in)
		require.Equal(t, stateDefault, h.state, "%q should dispatch and leave command mode", in)
		require.NotContains(t, h.cmdBar.String(), "unknown", "%q should be recognized", in)
	}
}

func TestExecuteCommand_UnknownStillFails(t *testing.T) {
	h := newNavTestHome(t)
	h.state = stateCommand
	h.executeCommand("definitely-not-a-command")
	require.Equal(t, stateCommand, h.state)
	require.Contains(t, h.cmdBar.String(), "unknown")
}

func TestSynthKeyMsg(t *testing.T) {
	require.Equal(t, "enter", synthKeyMsg("enter").String())
	require.Equal(t, "tab", synthKeyMsg("tab").String())
	require.Equal(t, "c", synthKeyMsg("c").String())
	require.Equal(t, "R", synthKeyMsg("R").String())
}
