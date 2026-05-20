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

- `session/` — `Instance`, `TmuxSession`, `GitWorktree`, storage; also the
  journal package (`session/journal/`), checkpoint/finish/doctor/summary
  layers, and the markdown task-record template (`finish_template.go`).
- `config/` — config + workspace registry/profiles
- `app/` — Bubble Tea TUI event loop
- `ui/` — list, preview, menu, tabs, overlays
- `daemon/` — autoyes daemon

## Subcommands the fork adds

Beyond `cs <no args>` (TUI) and `cs debug|reset|version|completion`, this fork
ships:

- `cs workspace ls|add|rm|edit` — manage the per-repo workspaces.
- `cs checkpoint [-m … | --interactive]` — record a signed checkpoint;
  designed to run inside a session pane, resolves identity via `CS_*` env vars.
- `cs finish` — closeout with the required-five audit payload (Intent / Work /
  Files / Verification / Disposition); refuses partial payloads. Flag form for
  agents, `--interactive` for humans (opens `$EDITOR` with a markdown template).
- `cs sessions` (alias `cs board`) — workspace-level rollup; `--tsv` for pipes.
- `cs doctor` — lint workspace journals (record-health, closure, staleness).
- `cs migrate-socket` — back-compat: recreate sessions on the dedicated
  `-L claudesquad` socket.

In-pane chord bindings (installed by `InstallSessionBindings` in
`session/tmux/tmux.go`):

- `<tmux-prefix> k`, `C-Space` → `cs checkpoint --interactive` in a popup.
- `<tmux-prefix> F`            → `cs finish --interactive` in a popup.

TUI keybindings live in `keys/keys.go`; the bottom-menu surfacing lives in
`ui/menu.go:keyMenuGroup`. New keys must be added to BOTH for them to render.
