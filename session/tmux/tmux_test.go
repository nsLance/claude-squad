package tmux

import (
	cmd2 "claude-squad/cmd"
	"claude-squad/log"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program", "")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program", "")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)

	// With workspace id, name is prefixed with the (truncated) workspace id.
	session = NewTmuxSession("asdf", "program", "abcdef0123456789")
	require.Equal(t, TmuxPrefix+"abcdef01_asdf", session.sanitizedName)
}

func TestPipePane_StartAndStop(t *testing.T) {
	var ran []string
	exe := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			ran = append(ran, cmd2.ToString(cmd))
			return nil
		},
	}
	session := newTmuxSession("piping", "claude", "", NewMockPtyFactory(t), exe)

	require.NoError(t, session.PipePane("/tmp/x with space.raw"))
	require.NoError(t, session.StopPipePane())

	require.Len(t, ran, 2)
	require.Equal(t,
		`tmux -L claudesquad pipe-pane -t claudesquad_piping cat >> '/tmp/x with space.raw'`,
		ran[0],
	)
	require.Equal(t,
		`tmux -L claudesquad pipe-pane -t claudesquad_piping`,
		ran[1],
	)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	// Start resolves the program against PATH; pin it so the expected
	// new-session command does not depend on what is installed on this host.
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", "", ptyFactory, cmdExec)

	err := session.Start(workdir, nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux -L claudesquad new-session -d -s claudesquad_test-session -c %s /usr/local/bin/claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux -L claudesquad attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}

func TestClose(t *testing.T) {
	t.Run("already-gone session is not an error", func(t *testing.T) {
		// Simulates an agent that exited on its own: kill-session fails because
		// the session is already gone, and has-session confirms it's absent.
		// Close must succeed so the caller can finish tearing the session down.
		exe := cmd_test.MockCmdExec{
			RunFunc: func(c *exec.Cmd) error {
				s := cmd2.ToString(c)
				if strings.Contains(s, "kill-session") {
					return fmt.Errorf("can't find session")
				}
				if strings.Contains(s, "has-session") {
					return fmt.Errorf("can't find session") // gone
				}
				return nil
			},
		}
		session := newTmuxSession("dead", "claude", "", NewMockPtyFactory(t), exe)
		require.NoError(t, session.Close())
	})

	t.Run("kill failure on a live session still errors", func(t *testing.T) {
		exe := cmd_test.MockCmdExec{
			RunFunc: func(c *exec.Cmd) error {
				s := cmd2.ToString(c)
				if strings.Contains(s, "kill-session") {
					return fmt.Errorf("boom")
				}
				if strings.Contains(s, "has-session") {
					return nil // still alive → genuine failure
				}
				return nil
			},
		}
		session := newTmuxSession("alive", "claude", "", NewMockPtyFactory(t), exe)
		require.Error(t, session.Close())
	})
}

func TestGracefulQuit(t *testing.T) {
	// Speed up the polling/key-spacing for the test.
	origSend, origPoll := keySendInterval, quitPollInterval
	keySendInterval, quitPollInterval = time.Millisecond, time.Millisecond
	defer func() { keySendInterval, quitPollInterval = origSend, origPoll }()

	t.Run("sends quit keys then returns once the session is gone", func(t *testing.T) {
		var sent []string
		hasSessionCalls := 0
		exe := cmd_test.MockCmdExec{
			RunFunc: func(c *exec.Cmd) error {
				s := cmd2.ToString(c)
				if strings.Contains(s, "send-keys") {
					sent = append(sent, s)
					return nil
				}
				if strings.Contains(s, "has-session") {
					hasSessionCalls++
					// Alive for the first two polls, then gone.
					if hasSessionCalls <= 2 {
						return nil
					}
					return fmt.Errorf("no such session")
				}
				return nil
			},
		}
		session := newTmuxSession("gq", "claude", "", NewMockPtyFactory(t), exe)

		err := session.GracefulQuit([]string{"C-c", "C-c"}, time.Second)
		require.NoError(t, err)
		require.Equal(t, []string{
			"tmux -L claudesquad send-keys -t claudesquad_gq C-c",
			"tmux -L claudesquad send-keys -t claudesquad_gq C-c",
		}, sent)
	})

	t.Run("returns error if the agent never exits", func(t *testing.T) {
		exe := cmd_test.MockCmdExec{
			RunFunc: func(c *exec.Cmd) error {
				return nil // send-keys ok; has-session always succeeds → still alive
			},
		}
		session := newTmuxSession("gq2", "claude", "", NewMockPtyFactory(t), exe)

		err := session.GracefulQuit([]string{"C-c"}, 10*time.Millisecond)
		require.Error(t, err)
		require.Contains(t, err.Error(), "did not exit")
	})

	t.Run("empty quitKeys defaults to double Ctrl-C", func(t *testing.T) {
		var sent []string
		exe := cmd_test.MockCmdExec{
			RunFunc: func(c *exec.Cmd) error {
				s := cmd2.ToString(c)
				if strings.Contains(s, "send-keys") {
					sent = append(sent, s)
				}
				if strings.Contains(s, "has-session") {
					return fmt.Errorf("gone")
				}
				return nil
			},
		}
		session := newTmuxSession("gq3", "claude", "", NewMockPtyFactory(t), exe)
		require.NoError(t, session.GracefulQuit(nil, time.Second))
		require.Len(t, sent, 2)
	})
}

func TestResolveProgramPath(t *testing.T) {
	log.Initialize(false) // resolveProgramPath logs a warning when lookup fails
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	require.NoError(t, os.WriteFile(codex, []byte("#!/bin/sh\n"), 0755))

	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(file string) (string, error) {
		if file == "codex" {
			return codex, nil
		}
		return "", fmt.Errorf("%s: not found", file)
	}

	for _, tc := range []struct {
		name    string
		program string
		want    string
	}{
		{"bare name resolves to absolute path", "codex", codex},
		{"flags are preserved", "codex --sandbox workspace-write", codex + " --sandbox workspace-write"},
		{"absolute path is left alone", "/opt/homebrew/bin/codex", "/opt/homebrew/bin/codex"},
		{"relative path is left alone", "./codex --flag", "./codex --flag"},
		{"unresolvable program is left alone", "nonesuch --flag", "nonesuch --flag"},
		{"empty program is left alone", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveProgramPath(tc.program))
		})
	}
}

// A resolved path containing spaces must be quoted, since tmux runs the pane
// command through a shell.
func TestResolveProgramPath_QuotesPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my tools")
	require.NoError(t, os.MkdirAll(dir, 0755))
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"), 0755))

	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return agent, nil }

	require.Equal(t, shellQuote(agent)+" --flag", resolveProgramPath("agent --flag"))
}

// Start must hand tmux the resolved path while leaving t.program — which the
// pane status heuristics match on — untouched.
func TestStart_ResolvesProgramButKeepsConfiguredName(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(claude, []byte("#!/bin/sh\n"), 0755))

	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return claude, nil }

	// has-session must report "absent" on Start's initial check and "present"
	// on the poll that follows, or Start either bails early or polls until it
	// times out.
	hasSessionCalls := 0
	exe := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			for _, a := range c.Args {
				if a == "has-session" {
					hasSessionCalls++
					if hasSessionCalls == 1 {
						return fmt.Errorf("no such session")
					}
					return nil
				}
			}
			return nil
		},
	}
	pty := NewMockPtyFactory(t)
	session := NewTmuxSessionWithDeps("s", ProgramClaude, "", pty, exe)
	// Start's own new-session goes through the pty factory, not cmdExec, so
	// inspect the command the factory was handed.
	require.NoError(t, session.Start(dir, []string{"CS_SESSION=s"}))

	require.NotEmpty(t, pty.cmds)
	joined := strings.Join(pty.cmds[0].Args, " ")
	require.Contains(t, joined, claude, "tmux should receive the resolved path")
	require.Equal(t, ProgramClaude, session.program, "configured program name must be preserved")
}
