package tmux

import (
	cmd2 "claude-squad/cmd"
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
	require.Equal(t, fmt.Sprintf("tmux -L claudesquad new-session -d -s claudesquad_test-session -c %s claude", workdir),
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
