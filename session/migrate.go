package session

import (
	"fmt"

	"claude-squad/log"
	"claude-squad/session/tmux"
)

// MigrateToDedicatedSocket moves inst's tmux session from tmux's default socket
// — where claude-squad builds predating the dedicated socket created it — onto
// claude-squad's dedicated socket.
//
// A live tmux session cannot be transferred between tmux servers, so the old
// session is killed and a fresh one is created in the same worktree (via
// Restart): the worktree, branch, and on-disk changes are preserved; the live
// agent process and its in-tmux scrollback are not. For claude-code the
// conversation is still resumable from its own per-directory transcript.
//
// Returns migrated=false (no error) when there is nothing on the default socket
// to migrate — the instance is paused, or already on the dedicated socket.
func MigrateToDedicatedSocket(inst *Instance) (migrated bool, err error) {
	if inst.Paused() {
		return false, nil
	}
	name := tmux.SessionName(inst.Title, inst.WorkspaceID)
	if !tmux.SessionExistsOnDefaultSocket(name) {
		return false, nil
	}
	if err := tmux.KillSessionOnDefaultSocket(name); err != nil {
		log.WarningLog.Printf("migrate %q: kill old session: %v", inst.Title, err)
	}
	if err := inst.Restart(); err != nil {
		return false, fmt.Errorf("recreate %q on the dedicated socket: %w", inst.Title, err)
	}
	return true, nil
}
