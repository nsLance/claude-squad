package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var cmdBarPromptStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("62"))

var cmdBarTextStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var cmdBarCursorStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("62"))

// CommandBar is the ":"-prompt input line. It manages only the typed buffer and
// an optional error message; the app owns parsing/dispatch and key routing
// (reusing the same rune-capture pattern as the new-session name entry).
type CommandBar struct {
	input         string
	err           string
	width, height int
}

func NewCommandBar() *CommandBar { return &CommandBar{} }

func (c *CommandBar) SetSize(w, h int) { c.width, c.height = w, h }

// Reset clears both the buffer and any error.
func (c *CommandBar) Reset() { c.input, c.err = "", "" }

func (c *CommandBar) Value() string { return c.input }

// SetError shows an error next to the prompt and keeps the buffer so the user
// can edit and retry.
func (c *CommandBar) SetError(s string) { c.err = s }

// Insert appends typed text and clears any prior error.
func (c *CommandBar) Insert(s string) {
	c.input += s
	c.err = ""
}

// Backspace removes the last rune and clears any prior error.
func (c *CommandBar) Backspace() {
	r := []rune(c.input)
	if len(r) > 0 {
		c.input = string(r[:len(r)-1])
	}
	c.err = ""
}

func (c *CommandBar) String() string {
	line := cmdBarPromptStyle.Render(":") +
		cmdBarTextStyle.Render(c.input) +
		cmdBarCursorStyle.Render("█")
	if c.err != "" {
		line += "   " + exitedStyle.Render(c.err)
	}
	h := c.height
	if h < 1 {
		h = 1
	}
	return lipgloss.Place(c.width, h, lipgloss.Left, lipgloss.Center, line)
}
