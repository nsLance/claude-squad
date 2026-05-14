package config

import (
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	tmuxNamePrefix      = "claudesquad_"
	workspaceShortIDLen = 8
)

type workspaceIDRename struct{ oldID, newID string }

// MigrateIDs rehashes every registered workspace's ID using the current
// WorkspaceID() scheme and moves on-disk state accordingly. Idempotent: a
// second run is a no-op. Best-effort: warnings logged for non-fatal failures
// rather than blocking startup.
//
// For each workspace whose stored ID disagrees with the freshly-derived one:
//   - the on-disk dir ~/.claude-squad/workspaces/<old>/ is renamed to .../<new>/
//   - state.json instance entries are rewritten (workspace_id field + any
//     worktree_path strings whose prefix matches the old dir)
//   - tmux sessions are renamed: claudesquad_<old8>_<title> → claudesquad_<new8>_<title>
//   - git's worktree admin gitdir pointers are rewritten so the source repo
//     knows where the worktree now lives
//   - the workspace's ID is updated in the registry and persisted
func (r *WorkspaceRegistry) MigrateIDs(state InstanceStorage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	root, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("migrate workspace ids: %w", err)
	}

	var renames []workspaceIDRename
	var dropIdx []int // indices to drop because they collide with a survivor and have no on-disk state
	seen := map[string]bool{}
	for _, ws := range r.Workspaces {
		seen[ws.ID] = true
	}
	for i := range r.Workspaces {
		ws := &r.Workspaces[i]
		newID := WorkspaceID(ws.RepoPath)
		if newID == ws.ID {
			continue
		}
		if seen[newID] {
			// Phantom duplicate (likely auto-registered during the URL-in-ID bug):
			// drop it if the on-disk dir is empty or missing; otherwise warn and keep.
			oldDir := filepath.Join(root, "workspaces", ws.ID)
			if isEmptyOrMissing(oldDir) {
				log.InfoLog.Printf("workspace id migration: removing duplicate registry entry %s (no on-disk state; collides with sibling at %s)", ws.ID, ws.RepoPath)
				dropIdx = append(dropIdx, i)
			} else {
				log.WarningLog.Printf("workspace id migration: %s → %s collides with an existing workspace (%s); leaving registry entry in place — resolve manually with `cs workspace rm`", ws.ID, newID, ws.RepoPath)
			}
			continue
		}
		renames = append(renames, workspaceIDRename{oldID: ws.ID, newID: newID})
		seen[newID] = true
		ws.ID = newID
	}
	if len(renames) == 0 && len(dropIdx) == 0 {
		return nil
	}

	if len(dropIdx) > 0 {
		kept := r.Workspaces[:0]
		drop := map[int]bool{}
		for _, i := range dropIdx {
			drop[i] = true
		}
		for i, ws := range r.Workspaces {
			if !drop[i] {
				kept = append(kept, ws)
			}
		}
		r.Workspaces = kept
	}

	for _, rn := range renames {
		oldDir := filepath.Join(root, "workspaces", rn.oldID)
		newDir := filepath.Join(root, "workspaces", rn.newID)
		if _, err := os.Stat(oldDir); err != nil {
			continue
		}
		if _, err := os.Stat(newDir); err == nil {
			log.WarningLog.Printf("workspace dir %s already exists; not overwriting", newDir)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(newDir), 0755); err != nil {
			log.WarningLog.Printf("mkdir parent for %s: %v", newDir, err)
			continue
		}
		if err := os.Rename(oldDir, newDir); err != nil {
			log.WarningLog.Printf("rename %s → %s: %v", oldDir, newDir, err)
			continue
		}
		log.InfoLog.Printf("workspace id migration: %s → %s (%s)", rn.oldID, rn.newID, newDir)
	}

	rewriteStateInstances(state, renames)

	for _, rn := range renames {
		newWorktreeDir := filepath.Join(root, "workspaces", rn.newID, "worktrees")
		repairGitWorktreesUnder(newWorktreeDir)
		renameTmuxSessionsForWorkspace(rn.oldID, rn.newID)
	}

	return r.saveLocked()
}

func rewriteStateInstances(state InstanceStorage, renames []workspaceIDRename) {
	if state == nil {
		return
	}
	raw := state.GetInstances()
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var instances []map[string]interface{}
	if err := json.Unmarshal(raw, &instances); err != nil {
		log.WarningLog.Printf("migrate state.json: unmarshal failed: %v (state.json left untouched)", err)
		return
	}
	changed := false
	for _, inst := range instances {
		wid, _ := inst["workspace_id"].(string)
		for _, rn := range renames {
			if wid == rn.oldID {
				inst["workspace_id"] = rn.newID
				wid = rn.newID
				changed = true
			}
		}
		wt, _ := inst["worktree"].(map[string]interface{})
		if wt == nil {
			continue
		}
		wp, _ := wt["worktree_path"].(string)
		if wp == "" {
			continue
		}
		for _, rn := range renames {
			oldFrag := string(filepath.Separator) + filepath.Join("workspaces", rn.oldID) + string(filepath.Separator)
			newFrag := string(filepath.Separator) + filepath.Join("workspaces", rn.newID) + string(filepath.Separator)
			if strings.Contains(wp, oldFrag) {
				wp = strings.Replace(wp, oldFrag, newFrag, 1)
				wt["worktree_path"] = wp
				changed = true
			}
		}
	}
	if !changed {
		return
	}
	out, err := json.Marshal(instances)
	if err != nil {
		log.WarningLog.Printf("migrate state.json: marshal failed: %v", err)
		return
	}
	if err := state.SaveInstances(out); err != nil {
		log.WarningLog.Printf("migrate state.json: save failed: %v", err)
	}
}

// repairGitWorktreesUnder walks newDir looking for `.git` files (the worktree
// pointer files) and rewrites the matching `<source-repo>/.git/worktrees/<name>/gitdir`
// file so the source repo knows the worktree's new path. Without this,
// `git worktree list` and most worktree operations from the source repo break.
func repairGitWorktreesUnder(newDir string) {
	_ = filepath.Walk(newDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) != ".git" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		const prefix = "gitdir:"
		content := strings.TrimSpace(string(data))
		if !strings.HasPrefix(content, prefix) {
			return nil
		}
		adminDir := strings.TrimSpace(strings.TrimPrefix(content, prefix))
		adminGitdir := filepath.Join(adminDir, "gitdir")
		expected := path + "\n"
		if existing, err := os.ReadFile(adminGitdir); err == nil && string(existing) == expected {
			return nil
		}
		if err := os.WriteFile(adminGitdir, []byte(expected), 0644); err != nil {
			log.WarningLog.Printf("repair worktree pointer %s: %v", adminGitdir, err)
		}
		return nil
	})
}

// renameTmuxSessionsForWorkspace renames any live tmux session whose name uses
// the old workspace prefix to use the new one. Mirrors the naming scheme in
// session/tmux.toClaudeSquadTmuxName.
func renameTmuxSessionsForWorkspace(oldID, newID string) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	oldShort := shortID(oldID)
	newShort := shortID(newID)
	oldNamePrefix := tmuxNamePrefix + oldShort + "_"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, oldNamePrefix) {
			continue
		}
		newName := tmuxNamePrefix + newShort + "_" + line[len(oldNamePrefix):]
		_ = exec.Command("tmux", "rename-session", "-t", line, newName).Run()
	}
}

func shortID(id string) string {
	if len(id) > workspaceShortIDLen {
		return id[:workspaceShortIDLen]
	}
	return id
}

// isEmptyOrMissing reports whether dir does not exist or contains no entries.
// Used to decide whether a colliding workspace's on-disk state is safe to drop.
func isEmptyOrMissing(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return os.IsNotExist(err)
	}
	return len(entries) == 0
}
