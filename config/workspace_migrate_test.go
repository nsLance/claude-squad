package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInstanceStorage is an in-memory InstanceStorage for migration tests.
type fakeInstanceStorage struct {
	data json.RawMessage
}

func (f *fakeInstanceStorage) GetInstances() json.RawMessage { return f.data }
func (f *fakeInstanceStorage) SaveInstances(b json.RawMessage) error {
	f.data = append(f.data[:0], b...)
	return nil
}
func (f *fakeInstanceStorage) DeleteAllInstances() error { f.data = nil; return nil }

// legacyURLBasedID reproduces the pre-migration ID derivation (sha256 over
// path + "\x00" + remoteURL, truncated to 6 bytes hex). Used to construct a
// realistic pre-migration registry without depending on the current code path.
func legacyURLBasedID(path, remoteURL string) string {
	h := sha256.Sum256([]byte(path + "\x00" + remoteURL))
	return hex.EncodeToString(h[:6])
}

func TestMigrateIDs_RenamesDirAndRewritesState(t *testing.T) {
	root := scopeConfigHome(t)
	repoPath := "/Users/x/projects/foo"
	oldID := legacyURLBasedID(repoPath, "https://upstream/foo.git")
	newID := WorkspaceID(repoPath)
	require.NotEqual(t, oldID, newID, "test premise: legacy hash and current hash must differ")

	// Build a workspaces.json with one workspace under the legacy ID.
	reg := &WorkspaceRegistry{Workspaces: []Workspace{{
		ID:          oldID,
		DisplayName: "foo",
		RepoPath:    repoPath,
		RemoteURL:   "https://upstream/foo.git",
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
	}}}
	require.NoError(t, reg.Save())

	// Lay down the old workspace dir + a fake worktree directory inside.
	oldWsDir := filepath.Join(root, "workspaces", oldID)
	wtRel := filepath.Join("worktrees", "nakkul", "foo_18abcdef")
	wtAbs := filepath.Join(oldWsDir, wtRel)
	require.NoError(t, os.MkdirAll(wtAbs, 0755))
	// Also lay down a sentinel file so we can confirm the dir moved intact.
	require.NoError(t, os.WriteFile(filepath.Join(oldWsDir, "sentinel.txt"), []byte("hi"), 0644))

	// Lay down a fake source-repo admin dir so the worktree pointer repair has
	// something to write to.
	srcRepoAdmin := filepath.Join(t.TempDir(), ".git", "worktrees", "foo_18abcdef")
	require.NoError(t, os.MkdirAll(srcRepoAdmin, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRepoAdmin, "gitdir"), []byte(wtAbs+"/.git\n"), 0644))
	// And the worktree's `.git` file pointing at that admin dir.
	require.NoError(t, os.WriteFile(filepath.Join(wtAbs, ".git"), []byte("gitdir: "+srcRepoAdmin+"\n"), 0644))

	// state.json with one instance referencing the old workspace.
	state := &fakeInstanceStorage{}
	stateJSON, err := json.Marshal([]map[string]interface{}{{
		"title":        "sess1",
		"workspace_id": oldID,
		"worktree": map[string]interface{}{
			"worktree_path": wtAbs,
		},
	}})
	require.NoError(t, err)
	require.NoError(t, state.SaveInstances(stateJSON))

	// Run migration.
	reloaded := LoadWorkspaceRegistry()
	require.NoError(t, reloaded.MigrateIDs(state))

	// Workspace registry: ID updated, single entry.
	assert.Len(t, reloaded.Workspaces, 1)
	assert.Equal(t, newID, reloaded.Workspaces[0].ID)

	// Persisted registry on disk matches.
	persisted := LoadWorkspaceRegistry()
	require.Len(t, persisted.Workspaces, 1)
	assert.Equal(t, newID, persisted.Workspaces[0].ID)

	// Dir was renamed; sentinel file moved with it.
	newWsDir := filepath.Join(root, "workspaces", newID)
	_, err = os.Stat(oldWsDir)
	assert.True(t, os.IsNotExist(err), "old workspace dir must be gone")
	_, err = os.Stat(filepath.Join(newWsDir, "sentinel.txt"))
	assert.NoError(t, err, "sentinel must have moved with the dir")

	// state.json: workspace_id and worktree_path rewritten.
	var instances []map[string]interface{}
	require.NoError(t, json.Unmarshal(state.GetInstances(), &instances))
	require.Len(t, instances, 1)
	assert.Equal(t, newID, instances[0]["workspace_id"])
	wt := instances[0]["worktree"].(map[string]interface{})
	assert.Equal(t, filepath.Join(newWsDir, wtRel), wt["worktree_path"])

	// Worktree admin pointer rewritten to the new path.
	gitdirBytes, err := os.ReadFile(filepath.Join(srcRepoAdmin, "gitdir"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(newWsDir, wtRel, ".git")+"\n", string(gitdirBytes))
}

func TestMigrateIDs_NoOpWhenAlreadyMigrated(t *testing.T) {
	scopeConfigHome(t)
	repoPath := "/Users/x/projects/foo"
	reg := &WorkspaceRegistry{Workspaces: []Workspace{{
		ID:       WorkspaceID(repoPath),
		RepoPath: repoPath,
	}}}
	require.NoError(t, reg.Save())

	reloaded := LoadWorkspaceRegistry()
	require.NoError(t, reloaded.MigrateIDs(nil))
	require.Len(t, reloaded.Workspaces, 1)
	assert.Equal(t, WorkspaceID(repoPath), reloaded.Workspaces[0].ID)
}
