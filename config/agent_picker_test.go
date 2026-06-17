package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// programs extracts the Program field of each profile, preserving order.
func programs(ps []Profile) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Program
	}
	return out
}

func TestMergedAgentProfiles_FreshUser(t *testing.T) {
	// nil cfg and nil ws, no default: just the built-in agents, sorted.
	got := MergedAgentProfiles(nil, nil, "")
	assert.Equal(t, []string{"aider", "claude", "codex", "gemini"}, programs(got))
}

func TestMergedAgentProfiles_DefaultFirst(t *testing.T) {
	got := MergedAgentProfiles(nil, nil, "codex")
	require.NotEmpty(t, got)
	assert.Equal(t, "codex", got[0].Program, "default program must come first")
	// codex still appears exactly once (deduped against the built-in).
	assert.Equal(t, []string{"codex", "aider", "claude", "gemini"}, programs(got))
}

func TestMergedAgentProfiles_DedupByProgram(t *testing.T) {
	// A custom-path claude profile must NOT collapse into the built-in "claude".
	cfg := &Config{
		DefaultProgram: "/opt/homebrew/bin/claude",
		Profiles: []Profile{
			{Name: "claude", Program: "/opt/homebrew/bin/claude"},
		},
	}
	got := MergedAgentProfiles(cfg, nil, "")
	progs := programs(got)
	assert.Contains(t, progs, "/opt/homebrew/bin/claude")
	assert.Contains(t, progs, "claude", "built-in claude survives alongside the custom path")
}

func TestMergedAgentProfiles_WorkspaceProfilesIncluded(t *testing.T) {
	ws := &Workspace{
		Profiles: []WorkspaceProfile{
			{Name: "team-codex", Program: "codex --team"},
		},
	}
	got := MergedAgentProfiles(nil, ws, "")
	progs := programs(got)
	assert.Equal(t, "codex --team", progs[0], "workspace profile precedes built-ins")
	assert.Contains(t, progs, "codex", "built-in codex still present")
}

func TestMergedAgentProfiles_NoDuplicates(t *testing.T) {
	cfg := &Config{DefaultProgram: "codex", Profiles: []Profile{{Name: "codex", Program: "codex"}}}
	ws := &Workspace{Profiles: []WorkspaceProfile{{Name: "codex", Program: "codex"}}}
	got := MergedAgentProfiles(cfg, ws, "codex")
	count := 0
	for _, p := range got {
		if p.Program == "codex" {
			count++
		}
	}
	assert.Equal(t, 1, count, "codex must appear exactly once despite many sources")
}

func TestSetDefaultAgent_RoundTrips(t *testing.T) {
	scopeConfigHome(t)
	reg := &WorkspaceRegistry{}
	ws := Workspace{ID: "abc123", DisplayName: "demo", RepoPath: "/tmp/demo"}
	require.NoError(t, reg.Upsert(ws))

	require.NoError(t, reg.SetDefaultAgent("abc123", "codex"))

	// Reload from disk to confirm it persisted.
	reloaded := LoadWorkspaceRegistry()
	got := reloaded.Get("abc123")
	require.NotNil(t, got)
	assert.Equal(t, "codex", got.DefaultAgent)
}

func TestSetDefaultAgent_UnknownIDNoError(t *testing.T) {
	scopeConfigHome(t)
	reg := &WorkspaceRegistry{}
	assert.NoError(t, reg.SetDefaultAgent("missing", "codex"))
}
