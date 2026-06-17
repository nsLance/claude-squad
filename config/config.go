package config

import (
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	ConfigFileName   = "config.json"
	defaultProgram   = "claude"
	ConfigHomeEnvVar = "CLAUDE_SQUAD_HOME"
)

// Version is claude-squad's own version string. It is set from main's version
// literal in main's init() and stamped into the journal header event. Lives
// here (not in main) so the session package can read it without importing main.
var Version = "dev"

// GetConfigDir returns the path to the application's configuration directory.
// If $CLAUDE_SQUAD_HOME is set, it overrides the default (~/.claude-squad).
func GetConfigDir() (string, error) {
	if v := os.Getenv(ConfigHomeEnvVar); v != "" {
		if strings.HasPrefix(v, "~") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to expand home directory: %w", err)
			}
			v = filepath.Join(homeDir, v[1:])
		}
		return v, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config home directory: %w", err)
	}
	return filepath.Join(homeDir, ".claude-squad"), nil
}

// Profile represents a named program configuration
type Profile struct {
	Name    string `json:"name"`
	Program string `json:"program"`
}

// AgentCommand customizes how the recycle ("rebuild") action treats one agent.
// Resume is the command used to relaunch the agent so it continues its prior
// conversation (e.g. "claude --continue"). QuitKeys is the tmux send-keys
// sequence sent to a still-running agent to ask it to shut down gracefully —
// letting it flush memory, commit, etc. — before it is relaunched; each entry
// is one tmux key token (e.g. "C-c", "Enter", or a literal like "/exit").
type AgentCommand struct {
	Resume   string   `json:"resume"`
	QuitKeys []string `json:"quit_keys"`
}

// Config represents the application configuration
type Config struct {
	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// AutoYes is a flag to automatically accept all prompts.
	AutoYes bool `json:"auto_yes"`
	// DaemonPollInterval is the interval (ms) at which the daemon polls sessions for autoyes mode.
	DaemonPollInterval int `json:"daemon_poll_interval"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// AgentCommands maps a base program name ("claude", "codex", ...) to its
	// recycle recipe (continue command + graceful-quit keys). Missing entries —
	// or missing fields within an entry — fall back to DefaultAgentCommands.
	AgentCommands map[string]AgentCommand `json:"agent_commands,omitempty"`
}

// DefaultAgentCommands returns the built-in recycle recipes, keyed by base
// program name. Used to fill any agent (or field) the user hasn't configured.
func DefaultAgentCommands() map[string]AgentCommand {
	dblCtrlC := []string{"C-c", "C-c"}
	return map[string]AgentCommand{
		"claude": {Resume: "claude --continue", QuitKeys: dblCtrlC},
		"codex":  {Resume: "codex resume --last", QuitKeys: dblCtrlC},
		"aider":  {Resume: "aider --restore-chat-history", QuitKeys: dblCtrlC},
		"gemini": {Resume: "gemini", QuitKeys: dblCtrlC},
	}
}

// baseProgram extracts the bare program name from a full launch command:
// "aider --model x" → "aider", "/opt/homebrew/bin/claude" → "claude".
func baseProgram(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// AgentCommandFor returns the recycle recipe for the given launch command,
// layering any user config over the built-in defaults. The Resume field falls
// back to the original program (relaunch as-is) and QuitKeys to a double Ctrl-C
// when neither config nor defaults know the agent.
func (c *Config) AgentCommandFor(program string) AgentCommand {
	base := baseProgram(program)
	def := DefaultAgentCommands()[base]

	ac := c.AgentCommands[base] // zero value if absent (nil map read is safe)
	if ac.Resume == "" {
		ac.Resume = def.Resume
	}
	if len(ac.QuitKeys) == 0 {
		ac.QuitKeys = def.QuitKeys
	}
	// Final fallbacks for an agent neither config nor defaults recognize.
	if ac.Resume == "" {
		ac.Resume = program
	}
	if len(ac.QuitKeys) == 0 {
		ac.QuitKeys = []string{"C-c", "C-c"}
	}
	return ac
}

// MergeResumeIntoProgram folds a launch program's path/flags into its resume
// recipe so a rebuilt ("recycled") session continues its prior conversation
// while still honoring the current profile. When the resume recipe targets the
// same base agent as the program — e.g. program "codex --sandbox workspace-write"
// and resume "codex resume --last" — the result keeps the full program and
// appends the resume's trailing args: "codex --sandbox workspace-write resume
// --last". A resume recipe for a different command (a custom wrapper) is left
// untouched, and an empty resume falls back to the program itself.
func MergeResumeIntoProgram(program, resume string) string {
	if resume == "" {
		return program
	}
	if program == "" {
		return resume
	}
	rFields := strings.Fields(resume)
	if len(rFields) == 0 || baseProgram(program) != baseProgram(resume) {
		return resume
	}
	suffix := strings.Join(rFields[1:], " ")
	if suffix == "" {
		return program
	}
	return program + " " + suffix
}

// GetProgram returns the program to run. If Profiles is non-empty and
// DefaultProgram matches a profile name, that profile's Program is returned.
// Otherwise DefaultProgram is returned as-is.
func (c *Config) GetProgram() string {
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			return p.Program
		}
	}
	return c.DefaultProgram
}

// GetProfiles returns a unified list of profiles. If Profiles is defined,
// those are returned with the default profile first. Otherwise, a single
// profile is synthesized from DefaultProgram.
func (c *Config) GetProfiles() []Profile {
	if len(c.Profiles) == 0 {
		return []Profile{{Name: c.DefaultProgram, Program: c.DefaultProgram}}
	}
	// Reorder so the default profile comes first.
	profiles := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			profiles = append(profiles, p)
			break
		}
	}
	for _, p := range c.Profiles {
		if p.Name != c.DefaultProgram {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// MergedAgentProfiles builds the deduped agent list offered by the interactive
// pickers: the built-in known agents (claude, codex, ...) merged with the user's
// global profiles and the in-context workspace's profiles. Dedup is by Program
// (first occurrence wins), so a custom "/opt/homebrew/bin/claude" and the
// built-in "claude" are kept as distinct choices. When defaultProgram is
// non-empty it is placed first so a picker's default cursor highlights it.
//
// Precedence (earlier wins on display name + ordering):
//  1. defaultProgram
//  2. workspace profiles (ws.Profiles)
//  3. global profiles (cfg.GetProfiles())
//  4. built-in agents (sorted for stable order)
//
// Either cfg or ws may be nil — a fresh user with no config still gets the
// built-in agents.
func MergedAgentProfiles(cfg *Config, ws *Workspace, defaultProgram string) []Profile {
	var out []Profile
	seen := map[string]bool{}
	add := func(name, program string) {
		if program == "" || seen[program] {
			return
		}
		if name == "" {
			name = program
		}
		seen[program] = true
		out = append(out, Profile{Name: name, Program: program})
	}

	if defaultProgram != "" {
		add(nameForProgram(cfg, ws, defaultProgram), defaultProgram)
	}
	if ws != nil {
		for _, p := range ws.Profiles {
			add(p.Name, p.Program)
		}
	}
	if cfg != nil {
		for _, p := range cfg.GetProfiles() {
			add(p.Name, p.Program)
		}
	}
	builtins := make([]string, 0, len(DefaultAgentCommands()))
	for name := range DefaultAgentCommands() {
		builtins = append(builtins, name)
	}
	sort.Strings(builtins)
	for _, name := range builtins {
		add(name, name)
	}
	return out
}

// nameForProgram returns a friendly display name for a program command by
// matching it against workspace then global profiles; falls back to the program
// itself when nothing matches.
func nameForProgram(cfg *Config, ws *Workspace, program string) string {
	if ws != nil {
		for _, p := range ws.Profiles {
			if p.Program == program {
				return p.Name
			}
		}
	}
	if cfg != nil {
		for _, p := range cfg.GetProfiles() {
			if p.Program == program {
				return p.Name
			}
		}
	}
	return program
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	program, err := GetClaudeCommand()
	if err != nil {
		log.ErrorLog.Printf("failed to get claude command: %v", err)
		program = defaultProgram
	}

	return &Config{
		DefaultProgram:     program,
		AutoYes:            false,
		DaemonPollInterval: 1000,
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.ErrorLog.Printf("failed to get current user: %v", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
	}
}

// GetClaudeCommand attempts to find the "claude" command in the user's shell
// It checks in the following order:
// 1. Shell alias resolution: using "which" command
// 2. PATH lookup
//
// If both fail, it returns an error.
func GetClaudeCommand() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash" // Default to bash if SHELL is not set
	}

	// Force the shell to load the user's profile and then run the command
	// For zsh, source .zshrc; for bash, source .bashrc
	var shellCmd string
	if strings.Contains(shell, "zsh") {
		shellCmd = "source ~/.zshrc &>/dev/null || true; which claude"
	} else if strings.Contains(shell, "bash") {
		shellCmd = "source ~/.bashrc &>/dev/null || true; which claude"
	} else {
		shellCmd = "which claude"
	}

	cmd := exec.Command(shell, "-c", shellCmd)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		path := strings.TrimSpace(string(output))
		if path != "" {
			// Check if the output is an alias definition and extract the actual path
			// Handle formats like "claude: aliased to /path/to/claude" or other shell-specific formats
			aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)
			matches := aliasRegex.FindStringSubmatch(path)
			if len(matches) > 1 {
				path = matches[1]
			}
			return path, nil
		}
	}

	// Otherwise, try to find in PATH directly
	claudePath, err := exec.LookPath("claude")
	if err == nil {
		return claudePath, nil
	}

	return "", fmt.Errorf("claude command not found in aliases or PATH")
}

func LoadConfig() *Config {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultConfig()
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default config if file doesn't exist
			defaultCfg := DefaultConfig()
			if saveErr := saveConfig(defaultCfg); saveErr != nil {
				log.WarningLog.Printf("failed to save default config: %v", saveErr)
			}
			return defaultCfg
		}

		log.WarningLog.Printf("failed to get config file: %v", err)
		return DefaultConfig()
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.ErrorLog.Printf("failed to parse config file: %v", err)
		return DefaultConfig()
	}

	return &config
}

// saveConfig saves the configuration to disk
func saveConfig(config *Config) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// SaveConfig exports the saveConfig function for use by other packages
func SaveConfig(config *Config) error {
	return saveConfig(config)
}
