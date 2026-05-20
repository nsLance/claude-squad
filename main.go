package main

import (
	"bufio"
	"claude-squad/app"
	cmd2 "claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/daemon"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/journal"
	"claude-squad/session/tmux"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	version       = "1.0.17"
	programFlag   string
	autoYesFlag   bool
	daemonFlag    bool
	workspaceFlag string
	binName       string
	rootCmd       = &cobra.Command{
		Use:   "claude-squad",
		Short: "Claude Squad - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log.Initialize(daemonFlag)
			defer log.Close()

			if daemonFlag {
				cfg := config.LoadConfig()
				wsID := os.Getenv(daemon.DaemonWorkspaceEnv)
				err := daemon.RunDaemon(cfg, wsID)
				log.ErrorLog.Printf("failed to start daemon %v", err)
				return err
			}

			reg := config.LoadWorkspaceRegistry()
			if err := reg.MigrateIDs(config.LoadState()); err != nil {
				log.WarningLog.Printf("workspace id migration: %v", err)
			}

			var workspace *config.Workspace
			switch {
			case workspaceFlag != "":
				workspace = reg.Get(workspaceFlag)
				if workspace == nil {
					workspace = reg.FindByName(workspaceFlag)
				}
				if workspace == nil {
					return fmt.Errorf("workspace not found: %s (use `cs workspace ls`)", workspaceFlag)
				}
				_ = reg.Touch(workspace.ID)
			default:
				currentDir, err := filepath.Abs(".")
				if err != nil {
					return fmt.Errorf("failed to get current directory: %w", err)
				}
				if git.IsGitRepo(currentDir) {
					workspace, err = resolveOrRegisterWorkspace(reg, currentDir)
					if err != nil {
						return fmt.Errorf("workspace auto-register: %w", err)
					}
				} else {
					// Not in a git repo — fall back to the most-recently-used
					// workspace if any exist. If none do, run with no active
					// workspace; the TUI surfaces a hint to add one with `A`.
					workspace = reg.MostRecentlyUsed()
				}
			}

			if err := migrateInstancesToWorkspaces(reg); err != nil {
				log.WarningLog.Printf("instance migration: %v", err)
			}

			cfg := config.LoadConfig()

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}
			// AutoYes flag overrides config
			autoYes := cfg.AutoYes
			if autoYesFlag {
				autoYes = true
			}
			if autoYes {
				defer func() {
					if err := daemon.LaunchDaemon(workspace.ID); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill this workspace's daemon if one is running, plus any legacy
			// pre-workspace daemon left over from before the upgrade.
			if err := daemon.StopDaemon(workspace.ID); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}
			if err := daemon.StopLegacyDaemon(); err != nil {
				log.WarningLog.Printf("failed to stop legacy daemon: %v", err)
			}

			var wsID string
			if workspace != nil {
				wsID = workspace.ID
			}
			return app.Run(ctx, program, autoYes, wsID)
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			if err := storage.DeleteAllInstances(); err != nil {
				return fmt.Errorf("failed to reset storage: %w", err)
			}
			fmt.Println("Storage has been reset successfully")

			if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
				return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
			}
			fmt.Println("Tmux sessions have been cleaned up")

			if err := git.CleanupWorktrees(); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}

			// Also clean up per-workspace worktree dirs. CleanupWorktrees only
			// handles the legacy global dir; post-workspaces every session's
			// worktree lives under that workspace's WorktreeRoot, and missing
			// these is what leaves orphans behind that block future sessions.
			reg := config.LoadWorkspaceRegistry()
			for _, w := range reg.Workspaces {
				root, err := w.WorktreeRoot()
				if err != nil {
					log.WarningLog.Printf("worktree root for workspace %s: %v", w.ID, err)
					continue
				}
				if err := git.CleanupWorkspaceWorktrees(w.RepoPath, root); err != nil {
					log.WarningLog.Printf("cleanup workspace %s worktrees: %v", w.ID, err)
				}
			}
			fmt.Println("Worktrees have been cleaned up")

			// Kill all per-workspace daemons and any legacy daemon.
			for _, w := range reg.Workspaces {
				if err := daemon.StopDaemon(w.ID); err != nil {
					log.WarningLog.Printf("failed to stop daemon for workspace %s: %v", w.ID, err)
				}
			}
			if err := daemon.StopLegacyDaemon(); err != nil {
				log.WarningLog.Printf("failed to stop legacy daemon: %v", err)
			}
			fmt.Println("daemons have been stopped")

			return nil
		},
	}

	migrateSocketCmd = &cobra.Command{
		Use:   "migrate-socket",
		Short: "Move existing sessions onto claude-squad's dedicated tmux socket",
		Long: "claude-squad now runs tmux on a dedicated socket. Sessions created by\n" +
			"earlier builds live on tmux's default socket and are invisible to the new\n" +
			"build. This recreates each of them on the dedicated socket in the same\n" +
			"worktree: the worktree, branch, and on-disk changes are kept; the live agent\n" +
			"process and in-tmux scrollback are not (tmux cannot move a session between\n" +
			"servers). Run this once, with no claude-squad TUI open.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			instances, err := storage.LoadInstances()
			if err != nil {
				return fmt.Errorf("failed to load sessions: %w", err)
			}

			var migrated, unchanged int
			for _, inst := range instances {
				ok, err := session.MigrateToDedicatedSocket(inst)
				switch {
				case err != nil:
					fmt.Printf("  ✗ %s: %v\n", inst.Title, err)
					unchanged++
				case ok:
					fmt.Printf("  ✓ %s\n", inst.Title)
					migrated++
				default:
					unchanged++
				}
			}
			if err := storage.SaveInstances(instances); err != nil {
				return fmt.Errorf("failed to save sessions: %w", err)
			}
			fmt.Printf("migrated %d session(s) to the dedicated socket; %d unchanged\n",
				migrated, unchanged)
			return nil
		},
	}

	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Print debug information like config paths, registered workspaces, and the effective env for each profile in the resolved workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			cfg := config.LoadConfig()
			configDir, err := config.GetConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get config directory: %w", err)
			}
			configJson, _ := json.MarshalIndent(cfg, "", "  ")
			fmt.Printf("Config: %s\n%s\n", filepath.Join(configDir, config.ConfigFileName), configJson)

			reg := config.LoadWorkspaceRegistry()
			if err := reg.MigrateIDs(config.LoadState()); err != nil {
				log.WarningLog.Printf("workspace id migration: %v", err)
			}
			fmt.Printf("\nWorkspaces: %s\n", filepath.Join(configDir, config.WorkspacesFileName))
			for _, w := range reg.Workspaces {
				fmt.Printf("  %s\n", w.String())
			}

			currentDir, err := filepath.Abs(".")
			if err == nil && git.IsGitRepo(currentDir) {
				ws, err := resolveOrRegisterWorkspace(reg, currentDir)
				if err == nil {
					fmt.Printf("\nResolved workspace for cwd: %s (%s)\n", ws.DisplayName, ws.ID)
					wsDir, _ := ws.Dir()
					wtRoot, _ := ws.WorktreeRoot()
					fmt.Printf("  dir:           %s\n", wsDir)
					fmt.Printf("  worktree root: %s\n", wtRoot)
					if ws.Hooks.PostWorktree != "" {
						fmt.Printf("  post_worktree: %s\n", ws.Hooks.PostWorktree)
					}
					for _, p := range ws.Profiles {
						fmt.Printf("\n  Profile %q -> %s\n", p.Name, p.Program)
						env, err := ws.ResolveEnv(&p)
						if err != nil {
							fmt.Printf("    (env resolution failed: %v)\n", err)
							continue
						}
						for _, kv := range env {
							fmt.Printf("    %s\n", kv)
						}
					}
				}
			}

			return nil
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s version %s\n", binName, version)
			fmt.Printf("https://github.com/smtg-ai/claude-squad/releases/tag/v%s\n", version)
		},
	}

	finishIntent              string
	finishWork                string
	finishFiles               []string
	finishNoFiles             bool
	finishVerificationStatus  string
	finishVerificationReason  string
	finishVerificationEvidence string
	finishDisposition         string
	finishInteractive         bool

	finishCmd = &cobra.Command{
		Use:   "finish",
		Short: "Close out the current session's journal with the required-five audit payload",
		Long: "Record a finish event in the journal of the claude-squad session this command runs " +
			"inside. Refuses to close without Intent, Work, Files Changed (or --no-files), a " +
			"Verification block, and a Disposition — the same gate miagent uses for " +
			"`miagent-task finish`. Resolves the session from the CS_* env vars.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			var f journal.Finish
			if finishInteractive {
				title := os.Getenv(session.EnvSession)
				if title == "" {
					title = "session"
				}
				var err error
				f, err = session.RunFinishInteractive(title, finishFiles)
				if err != nil {
					return err
				}
			} else {
				f = journal.Finish{
					Intent:       strings.TrimSpace(finishIntent),
					Work:         strings.TrimSpace(finishWork),
					FilesChanged: finishFiles,
					NoFiles:      finishNoFiles,
					Verification: &journal.Verification{
						Status:   finishVerificationStatus,
						Reason:   strings.TrimSpace(finishVerificationReason),
						Evidence: strings.TrimSpace(finishVerificationEvidence),
					},
					Disposition: finishDisposition,
				}
			}

			if err := session.FinishFromEnv(f); err != nil {
				return err
			}
			fmt.Printf("finish recorded: disposition=%s verification=%s\n",
				f.Disposition, f.Verification.Status)
			return nil
		},
	}

	sessionsTSV bool

	sessionsCmd = &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"board"},
		Short:   "List all sessions in the active workspace with their audit state",
		Long: "Walks the active workspace's journals and projects out one row " +
			"per session: status, last activity, agent, role/awaiting, " +
			"verification, disposition, intent. Use --tsv for shell pipelines.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			ws, err := resolveActiveWorkspace()
			if err != nil {
				return err
			}
			wsDir, err := ws.Dir()
			if err != nil {
				return err
			}
			summaries := session.SummarizeWorkspace(wsDir)
			if sessionsTSV {
				fmt.Println(session.SummaryTSVHeader())
				for _, s := range summaries {
					fmt.Println(session.FormatSummaryTSV(s))
				}
				return nil
			}
			session.PrintSummaryTable(os.Stdout, summaries)
			return nil
		},
	}

	doctorCmd = &cobra.Command{
		Use:   "doctor",
		Short: "Lint session journals in the active workspace",
		Long: "Walk every session journal in the active workspace and report " +
			"record-health, closure, and staleness findings. Exit code is 1 " +
			"if any error-severity finding is emitted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			ws, err := resolveActiveWorkspace()
			if err != nil {
				return err
			}
			wsDir, err := ws.Dir()
			if err != nil {
				return err
			}
			findings := session.RunDoctor(wsDir)
			for _, f := range findings {
				fmt.Println(session.FormatFinding(f))
			}
			if session.HasErrors(findings) {
				os.Exit(1)
			}
			return nil
		},
	}

	checkpointMessage     string
	checkpointInteractive bool

	checkpointCmd = &cobra.Command{
		Use:   "checkpoint",
		Short: "Record a signed checkpoint in the current session's journal",
		Long: "Record a signed checkpoint in the journal of the claude-squad session this " +
			"command runs inside. The session is resolved from the CS_* environment variables " +
			"claude-squad injects into every session, so this works from any agent CLI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			summary := strings.TrimSpace(checkpointMessage)
			if summary == "" && checkpointInteractive {
				fmt.Print("Checkpoint summary: ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				summary = strings.TrimSpace(line)
			}
			if summary == "" {
				return fmt.Errorf("a checkpoint summary is required (pass -m, or use --interactive)")
			}

			sig, err := session.CheckpointFromEnv(summary)
			if err != nil {
				return err
			}
			fmt.Printf("checkpoint recorded: %s\n", sig.Hash[:12])
			return nil
		},
	}

	workspaceCmd = &cobra.Command{
		Use:   "workspace",
		Short: "Manage claude-squad workspaces",
	}

	workspaceLsCmd = &cobra.Command{
		Use:   "ls",
		Short: "List registered workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()
			reg := config.LoadWorkspaceRegistry()
			for _, w := range reg.Workspaces {
				fmt.Println(w.String())
			}
			return nil
		},
	}

	workspaceAddCmd = &cobra.Command{
		Use:   "add <path>",
		Short: "Register a git repo as a workspace (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			reg := config.LoadWorkspaceRegistry()
			ws, err := resolveOrRegisterWorkspace(reg, abs)
			if err != nil {
				return err
			}
			fmt.Println(ws.String())
			return nil
		},
	}

	workspaceEditCmd = &cobra.Command{
		Use:   "edit",
		Short: "Open the workspace registry (workspaces.json) in $EDITOR",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()
			dir, err := config.GetConfigDir()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, config.WorkspacesFileName)
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			editCmd := exec.Command(editor, path)
			editCmd.Stdin = os.Stdin
			editCmd.Stdout = os.Stdout
			editCmd.Stderr = os.Stderr
			if err := editCmd.Run(); err != nil {
				return fmt.Errorf("editor exited with error: %w", err)
			}
			// Verify the file still parses; warn loudly if not so the user knows.
			if _, err := os.Stat(path); err == nil {
				if data, err := os.ReadFile(path); err == nil {
					var reg config.WorkspaceRegistry
					if err := json.Unmarshal(data, &reg); err != nil {
						return fmt.Errorf("workspaces.json no longer parses: %w (edits saved, but cs will load an empty registry until fixed)", err)
					}
				}
			}
			return nil
		},
	}

	workspaceRmCmd = &cobra.Command{
		Use:   "rm <id-or-name>",
		Short: "Remove a workspace from the registry (does not touch the repo or worktrees)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()
			reg := config.LoadWorkspaceRegistry()
			ws := reg.Get(args[0])
			if ws == nil {
				ws = reg.FindByName(args[0])
			}
			if ws == nil {
				return fmt.Errorf("workspace not found: %s", args[0])
			}
			return reg.Remove(ws.ID)
		},
	}
)

// resolveOrRegisterWorkspace finds the workspace for the git repo containing
// dirOrRepo, registering it silently if it doesn't exist.
// resolveActiveWorkspace picks the workspace `cs doctor`, `cs sessions`, and
// other workspace-scoped commands should operate on: --workspace flag if set,
// else the workspace owning the current directory, else the most-recently-
// used one. Returns an error when none of those resolves to a workspace.
func resolveActiveWorkspace() (*config.Workspace, error) {
	reg := config.LoadWorkspaceRegistry()
	if workspaceFlag != "" {
		if ws := reg.Get(workspaceFlag); ws != nil {
			return ws, nil
		}
		if ws := reg.FindByName(workspaceFlag); ws != nil {
			return ws, nil
		}
		return nil, fmt.Errorf("workspace not found: %s (use `cs workspace ls`)", workspaceFlag)
	}
	currentDir, err := filepath.Abs(".")
	if err != nil {
		return nil, err
	}
	if git.IsGitRepo(currentDir) {
		if ws, err := resolveOrRegisterWorkspace(reg, currentDir); err == nil {
			return ws, nil
		}
	}
	if ws := reg.MostRecentlyUsed(); ws != nil {
		return ws, nil
	}
	return nil, fmt.Errorf("no active workspace (use -W or run inside a registered repo)")
}

func resolveOrRegisterWorkspace(reg *config.WorkspaceRegistry, dirOrRepo string) (*config.Workspace, error) {
	root, err := git.FindGitRepoRoot(dirOrRepo)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonical = root
	}
	return reg.EnsureWorkspace(canonical, git.FirstRemoteURL(canonical))
}

// migrateInstancesToWorkspaces backfills WorkspaceID on any existing instances
// in state.json by deriving a workspace from each instance's worktree repo path.
// Operates on the raw JSON so it runs before instances are loaded/started.
func migrateInstancesToWorkspaces(reg *config.WorkspaceRegistry) error {
	state := config.LoadState()
	raw := state.GetInstances()
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var data []session.InstanceData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	changed := false
	for i := range data {
		if data[i].WorkspaceID != "" {
			continue
		}
		repoPath := data[i].Worktree.RepoPath
		if repoPath == "" {
			continue
		}
		canonical, err := filepath.EvalSymlinks(repoPath)
		if err != nil {
			canonical = repoPath
		}
		remote := git.FirstRemoteURL(canonical)
		id := config.WorkspaceID(canonical)
		if reg.Get(id) == nil {
			now := time.Now()
			_ = reg.Upsert(config.Workspace{
				ID:          id,
				DisplayName: filepath.Base(canonical),
				RepoPath:    canonical,
				RemoteURL:   remote,
				CreatedAt:   now,
				LastUsedAt:  now,
			})
		}
		data[i].WorkspaceID = id

		// Best-effort: rename any pre-existing tmux session from the legacy
		// (unprefixed) name to the new workspace-scoped name. Idempotent: only
		// fires when the legacy session exists and the new name is unused.
		oldName := tmux.SessionName(data[i].Title, "")
		newName := tmux.SessionName(data[i].Title, id)
		if oldName != newName {
			if tmux.Command("has-session", "-t", oldName).Run() == nil &&
				tmux.Command("has-session", "-t", newName).Run() != nil {
				_ = tmux.Command("rename-session", "-t", oldName, newName).Run()
			}
		}

		changed = true
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return state.SaveInstances(out)
}

func init() {
	// Publish the version to the config package so non-main packages (the
	// journal header) can stamp it without importing main.
	config.Version = version

	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")
	rootCmd.Flags().StringVar(&workspaceFlag, "workspace", "",
		"Workspace id or display name. If set, use that workspace instead of auto-resolving from the current directory.")

	// Hide the daemonFlag as it's only for internal use
	err := rootCmd.Flags().MarkHidden("daemon")
	if err != nil {
		panic(err)
	}

	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(migrateSocketCmd)

	checkpointCmd.Flags().StringVarP(&checkpointMessage, "message", "m", "",
		"Checkpoint summary (one line)")
	checkpointCmd.Flags().BoolVar(&checkpointInteractive, "interactive", false,
		"Prompt for the summary on stdin (used by the in-pane checkpoint key binding)")
	rootCmd.AddCommand(checkpointCmd)

	rootCmd.AddCommand(doctorCmd)

	sessionsCmd.Flags().BoolVar(&sessionsTSV, "tsv", false,
		"Emit one tab-separated row per session, prefixed with a header line")
	rootCmd.AddCommand(sessionsCmd)

	finishCmd.Flags().StringVar(&finishIntent, "intent", "",
		"What this session was trying to accomplish (required)")
	finishCmd.Flags().StringVar(&finishWork, "work", "",
		"Summary of what was done — commands run, edits made, key outputs (required)")
	finishCmd.Flags().StringArrayVar(&finishFiles, "files", nil,
		"Path that changed, repeatable. Required unless --no-files is set.")
	finishCmd.Flags().BoolVar(&finishNoFiles, "no-files", false,
		"Explicitly assert that no files changed (mirrors miagent's 'no files changed')")
	finishCmd.Flags().StringVar(&finishVerificationStatus, "verification", "",
		"Verification status: not-run | partial | passed | failed | n/a (required)")
	finishCmd.Flags().StringVar(&finishVerificationReason, "verification-reason", "",
		"Required when --verification=not-run")
	finishCmd.Flags().StringVar(&finishVerificationEvidence, "verification-evidence", "",
		"Freeform evidence: command output, test results, manual checks")
	finishCmd.Flags().StringVar(&finishDisposition, "disposition", "",
		"Final disposition: merged | abandoned | handed-off | other (required)")
	finishCmd.Flags().BoolVar(&finishInteractive, "interactive", false,
		"Open $EDITOR with a markdown task-record template; flag values that are "+
			"set still seed defaults (e.g. --files pre-fills the Files Changed section)")
	rootCmd.AddCommand(finishCmd)

	workspaceCmd.AddCommand(workspaceLsCmd)
	workspaceCmd.AddCommand(workspaceAddCmd)
	workspaceCmd.AddCommand(workspaceRmCmd)
	workspaceCmd.AddCommand(workspaceEditCmd)
	rootCmd.AddCommand(workspaceCmd)
}

func main() {
	// Extract the binary name from how this was invoked
	binName = filepath.Base(os.Args[0])
	rootCmd.Use = binName

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
