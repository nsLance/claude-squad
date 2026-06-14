# Claude Squad [![CI](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml/badge.svg)](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml) [![GitHub Release](https://img.shields.io/github/v/release/smtg-ai/claude-squad)](https://github.com/smtg-ai/claude-squad/releases/latest)

[Claude Squad](https://smtg-ai.github.io/claude-squad/) is a terminal app that manages multiple [Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini](https://github.com/google-gemini/gemini-cli) (and other local agents including [Aider](https://github.com/Aider-AI/aider)) in separate workspaces, allowing you to work on multiple tasks simultaneously.


![Claude Squad Screenshot](assets/screenshot.png)

### Highlights
- Complete tasks in the background (including yolo / auto-accept mode!)
- Manage instances and tasks in one terminal window
- Review changes before applying them, checkout changes before pushing them
- Each task gets its own isolated git workspace, so no conflicts

<br />

https://github.com/user-attachments/assets/aef18253-e58f-4525-9032-f5a3d66c975a

<br />

### Installation

Both Homebrew and manual installation will install Claude Squad as `cs` on your system.

#### Homebrew

```bash
brew install claude-squad
ln -s "$(brew --prefix)/bin/claude-squad" "$(brew --prefix)/bin/cs"
```

#### Manual

Claude Squad can also be installed by running the following command:

```bash
curl -fsSL https://raw.githubusercontent.com/smtg-ai/claude-squad/main/install.sh | bash
```

This puts the `cs` binary in `~/.local/bin`.

To use a custom name for the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/smtg-ai/claude-squad/main/install.sh | bash -s -- --name <your-binary-name>
```

### Prerequisites

- [tmux](https://github.com/tmux/tmux/wiki/Installing)
- [gh](https://cli.github.com/)

### Usage

```
Usage:
  cs [flags]
  cs [command]

Available Commands:
  board           Alias for `sessions`
  checkpoint      Record a signed checkpoint in the current session's journal
  completion      Generate the autocompletion script for the specified shell
  debug           Print debug information like config paths
  doctor          Lint session journals in the active workspace
  finish          Close out the current session's journal with the required-five audit payload
  help            Help about any command
  migrate-socket  Recreate any sessions on the dedicated tmux socket
  reset           Reset all stored instances
  sessions        List every session in the active workspace with its audit state
  version         Print the version number of claude-squad
  workspace       Manage claude-squad workspaces

Flags:
  -y, --autoyes            [experimental] If enabled, all instances will automatically accept prompts for claude code & aider
  -h, --help               help for claude-squad
  -p, --program string     Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')
  -W, --workspace string   Workspace id or name to operate on (auto-detected from CWD if unset)
```

Run the application with:

```bash
cs
```
NOTE: The default program is `claude` and we recommend using the latest version.

<br />

<b>Using Claude Squad with other AI assistants:</b>
- For [Codex](https://github.com/openai/codex): Set your API key with `export OPENAI_API_KEY=<your_key>`
- Launch with specific assistants:
   - Codex: `cs -p "codex"`
   - Aider: `cs -p "aider ..."`
   - Gemini: `cs -p "gemini"`
- Make this the default, by modifying the config file (locate with `cs debug`)

<br />

#### Interface (k9s-style)

The UI is a **table-primary, drill-down** interface modeled on [k9s], mapping
workspaces → namespaces and sessions → pods:

- A **top banner** shows context (version, active workspace as `ns:`, session/workspace counts) with hotkey hints, plus a **breadcrumb** of the current view.
- The main area is a **columnar table** (sortable, with a `▸` cursor): a workspaces list, or a sessions list (`NAME / WORKSPACE / BRANCH / STATUS / DIFF / AGE`).
- **Drill down** with `↵` (workspace → its sessions → session detail) and **back out** with `esc`.

[k9s]: https://k9scli.io/

##### Command bar (`:`)
Press `:` to open the command bar (the primary way to jump around):
- `:workspaces` (`:ns`) — the workspaces ("namespace") list
- `:ws <name>` — jump into a workspace's sessions
- `:sessions` (`:s`, `:all`) — all sessions, unscoped
- `:new` (`:n`) — create (a session inside a workspace, a workspace on the list)
- `:quit` (`:q`) — quit

Every keybinding is also runnable as a command, case-sensitively: `:c` (checkout), `:p` (push), `:r` (resume), `:R` (restart), `:F` (finish), `:A` (add workspace), plus word forms like `:checkout`, `:push`.

##### Create / delete (context-aware, one key each)
`n` and `D` act on whatever the current view lists:
- On the **workspaces** list: `n` creates a workspace, `D` deletes one (refused while it still has sessions).
- Inside a **workspace** (after `↵`): `n` opens the session create form (name → prompt/branch/profile), `D` deletes the selected session.

The create form has sensible defaults — type a name and press `Enter` straight through to create on a new branch with the workspace's default profile; `Tab` to set a prompt, pick a branch, or choose a profile.

##### Session Management
- `R` - Restart a session whose agent process exited (keeps the worktree)
- `↑/j`, `↓/k` - Move the cursor

##### Actions
- `↵/o` - Drill in / attach to the selected session
- `ctrl-q` - Detach from session
- `p` - Commit and push branch to github
- `c` - Checkout. Commits changes and pauses the session
- `r` - Resume a paused session
- `F` - Finish: opens `$EDITOR` with a closeout template; records the audit payload to the session journal
- `?` - Show help menu

##### Workspaces
- `↵` - Enter a workspace to see and create its sessions
- `A` - Add a workspace from anywhere (existing dir or new — git-init's new dirs automatically)

##### Other
- `tab` - Switch between preview, diff, and terminal tabs
- `q` - Quit the application
- `shift-↓/↑` - scroll in preview/diff/terminal

##### In-pane chord bindings (work inside any agent CLI running in a cs session)
Because the bindings live on the dedicated tmux server, they fire before the
pane sees the key — they work from inside claude, codex, aider, or a plain shell.

- `<tmux-prefix> k` or `C-Space` - Open `cs checkpoint --interactive` in a popup
- `<tmux-prefix> F`              - Open `cs finish --interactive` in a popup

### Audit journal

Every session keeps an append-only JSONL journal under
`~/.claude-squad/workspaces/<id>/sessions/<slug>/journal.jsonl`, symlinked into
the worktree at `.cs/journal.jsonl`. The journal records prompts (captured
passively from supported agents), notes, signed checkpoints, decisions,
handoffs, and the final closeout — enough to reconstruct who decided what
across multiple agents working the same task.

Supported agents and what gets captured:

| Agent       | Prompts captured | Source                                                |
|-------------|------------------|-------------------------------------------------------|
| claude-code | yes              | `~/.claude/projects/<encoded-cwd>/*.jsonl`            |
| codex       | yes              | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`         |
| aider / gemini / shell | no    | raw pane bytes still capture in `transcript.raw`      |

Every session also gets a `transcript.raw` next to the journal that captures
the rendered pane bytes via `tmux pipe-pane` — the LLM-agnostic safety net
for agents without a structured-transcript adapter.

##### CLI commands inside a session pane

Every cs session injects `CS_*` env vars into its tmux pane, so these resolve
the right journal automatically without flags:

| Command                            | What it does                                                                       |
|------------------------------------|------------------------------------------------------------------------------------|
| `cs checkpoint -m "<summary>"`     | Record a signed checkpoint chained to the previous one. `--interactive` prompts.   |
| `cs finish --interactive`          | Open `$EDITOR` with a task-record template; close out the session with required Intent / Work / Files / Verification / Disposition. Refuses to record an incomplete payload — same gate as miagent's `miagent-task finish`. |
| `cs finish --intent ... --work ... --files ... --verification ... --disposition ...` | Same gate, flag-driven; suitable for an agent or script. |

##### CLI commands from outside a session

| Command           | What it does                                                                       |
|-------------------|------------------------------------------------------------------------------------|
| `cs sessions`     | One row per session in the active workspace: status, last activity, agent, role/awaiting, verification, intent. `--tsv` for shell pipelines. Aliased as `cs board`. |
| `cs doctor`       | Lint workspace journals: malformed JSON, missing header, unknown event types, closure (no finish yet), staleness (no finish + idle > 14d). Exit 1 on any error-severity finding. |
| `cs workspace ls` | List registered workspaces.                                                       |
| `cs workspace add <path>` | Register a git repo as a workspace (idempotent).                          |

### Configuration

Claude Squad stores its configuration in `~/.claude-squad/config.json`. You can find the exact path by running `cs debug`.

#### Profiles

Profiles let you define multiple named program configurations and switch between them when creating a new session. When more than one profile is defined, the session creation overlay shows a profile picker that you can navigate with `←`/`→`.

To configure profiles, add a `profiles` array to your config file and set `default_program` to the name of the profile to select by default:

```json
{
  "default_program": "claude",
  "profiles": [
    { "name": "claude", "program": "claude" },
    { "name": "codex", "program": "codex" },
    { "name": "aider", "program": "aider --model ollama_chat/gemma3:1b" }
  ]
}
```

Each profile has two fields:

| Field     | Description                                              |
|-----------|----------------------------------------------------------|
| `name`    | Display name shown in the profile picker                 |
| `program` | Shell command used to launch the agent for that profile  |

If no profiles are defined, Claude Squad uses `default_program` directly as the launch command (the default is `claude`).

### FAQs

#### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the
underlying program (ex. `claude`) to the latest version.

### How It Works

1. **tmux** to create isolated terminal sessions for each agent
2. **git worktrees** to isolate codebases so each session works on its own branch
3. A simple TUI interface for easy navigation and management

### License

[AGPL-3.0](LICENSE.md)

### Star History

[![Star History Chart](https://api.star-history.com/svg?repos=smtg-ai/claude-squad&type=Date)](https://www.star-history.com/#smtg-ai/claude-squad&Date)
