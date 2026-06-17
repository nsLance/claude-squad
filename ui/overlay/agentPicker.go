package overlay

import (
	"claude-squad/config"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentPickerOverlay is a small standalone overlay that wraps a ProfilePicker
// to choose a workspace's default agent. Enter submits, Esc cancels. It reuses
// the text-input overlay's border/title styling (tiStyle/tiTitleStyle) for a
// consistent look.
type AgentPickerOverlay struct {
	picker    *ProfilePicker
	Submitted bool
	Canceled  bool
	width     int
}

// NewAgentPickerOverlay builds the overlay from the given profiles, opening with
// the cursor on selectedProgram (if it matches one of them).
func NewAgentPickerOverlay(profiles []config.Profile, selectedProgram string) *AgentPickerOverlay {
	pp := NewProfilePicker(profiles)
	pp.SetSelectedByProgram(selectedProgram)
	pp.Focus()
	return &AgentPickerOverlay{picker: pp}
}

// SetWidth sets the rendering width.
func (a *AgentPickerOverlay) SetWidth(w int) {
	a.width = w
	a.picker.SetWidth(w)
}

// HandleKeyPress processes a key event. Returns true when the overlay should
// close (on submit or cancel).
func (a *AgentPickerOverlay) HandleKeyPress(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEnter:
		a.Submitted = true
		return true
	case tea.KeyEsc:
		a.Canceled = true
		return true
	}
	a.picker.HandleKeyPress(msg)
	return false
}

// GetSelectedProgram returns the program command of the chosen agent.
func (a *AgentPickerOverlay) GetSelectedProgram() string {
	return a.picker.GetSelectedProfile().Program
}

// Render renders the overlay.
func (a *AgentPickerOverlay) Render() string {
	title := tiTitleStyle.Render("Set default agent — ←/→ to choose · Enter to save · Esc to cancel")
	return tiStyle.Render(title + "\n" + a.picker.Render())
}
