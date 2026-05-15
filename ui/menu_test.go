package ui

import (
	"claude-squad/keys"
	"claude-squad/session"
	"testing"
)

func menuHasOption(opts []keys.KeyName, want keys.KeyName) bool {
	for _, k := range opts {
		if k == want {
			return true
		}
	}
	return false
}

// TestMenu_RestartOptionVisibleOnlyWhenExited verifies the menu surfaces the
// "restart" action exactly for sessions whose agent process has exited — not
// for healthy sessions, and not in place of "resume" for paused ones.
func TestMenu_RestartOptionVisibleOnlyWhenExited(t *testing.T) {
	inst := newTestInstance(t, "s", "")
	m := NewMenu()

	inst.SetStatus(session.Ready)
	m.SetInstance(inst)
	if menuHasOption(m.options, keys.KeyRestart) {
		t.Errorf("restart must be hidden for a live (Ready) session: %v", m.options)
	}

	inst.SetStatus(session.Exited)
	m.SetInstance(inst)
	if !menuHasOption(m.options, keys.KeyRestart) {
		t.Errorf("restart must be surfaced for an Exited session: %v", m.options)
	}
	if menuHasOption(m.options, keys.KeyResume) {
		t.Errorf("resume is for paused sessions, not exited ones: %v", m.options)
	}

	inst.SetStatus(session.Paused)
	m.SetInstance(inst)
	if menuHasOption(m.options, keys.KeyRestart) {
		t.Errorf("restart must be hidden for a paused session (use resume): %v", m.options)
	}
	if !menuHasOption(m.options, keys.KeyResume) {
		t.Errorf("resume must be surfaced for a paused session: %v", m.options)
	}
}
