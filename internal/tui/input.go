package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
)

// promptState is the bottom prompt field: a single-line input (title) or a
// growing multiline area (objective, findings). Enter submits, Esc cancels;
// Ctrl+J inserts a newline in multiline mode (Shift+Enter where the
// terminal supports it).
type promptState struct {
	label     string
	hint      string
	multiline bool
	input     textinput.Model
	area      textarea.Model
	onSubmit  func(a *App, text string) tea.Cmd
	onCancel  func(a *App) tea.Cmd
}

func newLinePrompt(label, hint string, onSubmit func(*App, string) tea.Cmd) *promptState {
	ti := textinput.New()
	ti.Prompt = " "
	ti.Focus()
	return &promptState{label: label, hint: hint, input: ti, onSubmit: onSubmit}
}

func newAreaPrompt(label, hint string, onSubmit func(*App, string) tea.Cmd) *promptState {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "shift+enter")
	ta.Focus()
	return &promptState{label: label, hint: hint, multiline: true, area: ta, onSubmit: onSubmit}
}

func (a *App) updatePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := a.prompt
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			a.prompt = nil
			if p.onCancel != nil {
				return a, p.onCancel(a)
			}
			return a, nil
		case "enter":
			var text string
			if p.multiline {
				text = strings.TrimSpace(p.area.Value())
			} else {
				text = strings.TrimSpace(p.input.Value())
			}
			a.prompt = nil
			return a, p.onSubmit(a, text)
		}
	}
	var cmd tea.Cmd
	if p.multiline {
		p.area, cmd = p.area.Update(msg)
	} else {
		p.input, cmd = p.input.Update(msg)
	}
	return a, cmd
}

func (p *promptState) view() string {
	var b strings.Builder
	b.WriteString(" " + stTitle.Render(p.label) + "\n")
	if p.multiline {
		b.WriteString(p.area.View() + "\n")
	} else {
		b.WriteString(p.input.View() + "\n")
	}
	b.WriteString(" " + stDim.Render(p.hint) + "\n")
	return b.String()
}

// confirmState is the one-line Enter/Esc confirmation in the status area.
type confirmState struct {
	text string
	yes  func(a *App) tea.Cmd
}

func (a *App) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	switch key.String() {
	case "enter":
		c := a.confirm
		a.confirm = nil
		return a, c.yes(a)
	case "esc", "n", "q":
		a.confirm = nil
	}
	return a, nil
}

// startCreatePrompt runs the two-step creation flow: the task is born on the
// first Enter; the Objective is an optional second stroke.
func (a *App) startCreatePrompt(backlog bool) tea.Cmd {
	a.prompt = newLinePrompt("New task — title:",
		"Enter — create · Esc — cancel",
		func(a *App, title string) tea.Cmd {
			if title == "" {
				return a.flash(stErr, "✗ empty title — nothing created")
			}
			task, _, err := a.ws.CreateTask(title, core.CreateOpts{Backlog: backlog})
			if err != nil {
				return a.flash(stErr, "✗ %v", err)
			}
			a.reload()
			if a.bl != nil {
				a.refreshBacklog()
			}
			for i, r := range a.rows {
				if r.ID == task.Meta.ID {
					a.cur = i
				}
			}
			id, dir := task.Meta.ID, task.Dir
			a.prompt = newAreaPrompt(
				id+" · Objective — a short draft of the idea:",
				"Enter — save (may be empty) · Ctrl+J — new line · Esc — skip",
				func(a *App, text string) tea.Cmd {
					if text != "" {
						if t, err := core.LoadTask(dir); err == nil {
							t.SetObjective(text)
						}
					}
					return a.flash(stOK, "✔ %s created", id)
				})
			return nil
		})
	return nil
}
