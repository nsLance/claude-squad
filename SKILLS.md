# SKILLS.md

Agent guidance for this repo — nakkul's fork of `smtg-ai/claude-squad`.
Local `main` carries feature work not yet upstream (workspaces, soft-reset,
session journal, etc.).

## Verify every change

Before considering any code change done, run all three from the repo root:

```sh
go build ./...
go vet ./...
go test ./...
```

## Ship it to the test binary (do this after a build, every time)

The user tests features from a separate binary, `cs-edge`, built in a
sibling git worktree. **`cs-edge` builds from committed `main`** — so
uncommitted changes will NOT appear in the test binary.

After a change is verified and ready for the user to try, do BOTH steps:

1. **Commit to `main`.** The work must be committed (the `cs-edge` build
   checks out `main`; a dirty working tree in this worktree is invisible to
   it). Keep commits focused; split unrelated features into separate commits.
2. **Run `cs-edge-update`.** This is a shell function in `~/.zshrc`:

   ```sh
   (cd ~/Workspace/claude-squad-edge && git checkout --detach main && go build -o cs-edge .)
   ```

   It rebuilds `~/Workspace/claude-squad-edge/cs-edge` from the latest
   committed `main`. Run the commands directly if the function isn't loaded
   (non-interactive shells don't source `~/.zshrc`).

Skipping step 1 means `cs-edge` silently builds stale code and the user
tests the wrong thing — always commit first, then rebuild `cs-edge`.

## Layout

- `session/` — `Instance`, `TmuxSession`, `GitWorktree`, storage
- `config/` — config + workspace registry/profiles
- `app/` — Bubble Tea TUI event loop
- `ui/` — list, preview, menu, tabs, overlays
- `daemon/` — autoyes daemon
