package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
)

// The staircase board: one row per task, indented by stage index; stage
// headers sit in a single truncated line above.

func (a *App) updateBoard(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	switch key.String() {
	case "q", "ctrl+c":
		return a, tea.Quit
	case "up", "k":
		if a.cur > 0 {
			a.cur--
		}
	case "down", "j":
		if a.cur < len(a.rows)-1 {
			a.cur++
		}
	case "tab", "shift+tab":
		if r, ok := a.selected(); ok {
			return a, a.moveStage(r, key.String() == "tab")
		}
	case "enter":
		if r, ok := a.selected(); ok {
			a.openCard(r.ID)
		}
	case "n":
		return a, a.startCreatePrompt(false)
	case "b":
		if r, ok := a.selected(); ok {
			id := r.ID
			a.confirm = &confirmState{
				text: fmt.Sprintf("send %s %q to the backlog?", r.ID, r.Title),
				yes: func(a *App) tea.Cmd {
					res, err := a.ws.MoveTask(id, core.Backlog, "user", "")
					a.reload()
					if err != nil {
						return a.flash(stErr, "✗ %v", err)
					}
					return a.flash(stOK, "✔ %s → backlog", res.ID)
				},
			}
		}
	case "B":
		a.openBacklog()
	case "S":
		a.openSprint()
	case "W":
		return a, a.openWsPick()
	case ",":
		a.openRoles()
	}
	return a, nil
}

func (a *App) selected() (row, bool) {
	if a.cur < 0 || a.cur >= len(a.rows) {
		return row{}, false
	}
	return a.rows[a.cur], true
}

// moveStage runs the Tab/⇧Tab transition through the core validator.
func (a *App) moveStage(r row, forward bool) tea.Cmd {
	var to string
	var ok bool
	if forward {
		to, ok = a.ws.NextStage(r.Stage)
	} else {
		to, ok = a.ws.PrevStage(r.Stage)
	}
	if !ok {
		return a.flash(stErr, "✗ %s: nowhere to go from %s", r.ID, r.Stage)
	}
	res, err := a.ws.MoveTask(r.ID, to, "user", "")
	a.reload()
	if err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	// Keep the cursor on the moved task.
	for i, x := range a.rows {
		if x.ID == res.ID {
			a.cur = i
		}
	}
	return a.flash(stOK, "✔ %s: %s → %s", res.ID, res.From, res.To)
}

func (a *App) stageStep() int {
	n := len(a.ws.WF.Stages)
	step := 8
	for step > 2 && (n-1)*step+44 > max(a.w, 40) {
		step /= 2
	}
	return step
}

func (a *App) viewBoard() string {
	var b strings.Builder
	step := a.stageStep()

	// Header.
	head := ""
	if active, err := a.ws.ActiveSprint(); err == nil {
		day := int(time.Since(active.Meta.StartedAt).Hours()/24) + 1
		head = fmt.Sprintf("%s · day %d/%d · closes %s", active.Name, day,
			active.Meta.LengthDays, active.Meta.EndsAt.Local().Format("02.01"))
	}
	if a.repo != "" {
		head += " · " + baseName(a.repo)
	}
	head += fmt.Sprintf(" · %d tasks", len(a.rows))
	if a.backlogN > 0 {
		head += stDim.Render(fmt.Sprintf(" (+%d in backlog)", a.backlogN))
	}
	b.WriteString(" " + head + "\n\n")

	// Stage header line (staircase positions, truncated labels).
	selStage := -1
	if r, ok := a.selected(); ok {
		selStage = r.StageIdx
	}
	line := make([]rune, 0, a.w)
	for i, s := range a.ws.WF.Stages {
		label := truncate(s.ID, step-1)
		for len(line) < i*step {
			line = append(line, ' ')
		}
		line = append(line, []rune(label)...)
	}
	// Render with the selected stage highlighted.
	rendered := string(line)
	if selStage >= 0 {
		parts := []string{}
		for i, s := range a.ws.WF.Stages {
			label := truncate(s.ID, step-1)
			pad := ""
			if i > 0 {
				pad = strings.Repeat(" ", max(0, step-len([]rune(truncate(a.ws.WF.Stages[i-1].ID, step-1)))))
			}
			if i == selStage {
				parts = append(parts, pad+stHeadSel.Render(label))
			} else {
				parts = append(parts, pad+stDim.Render(label))
			}
		}
		rendered = strings.Join(parts, "")
	}
	b.WriteString(" " + rendered + "\n\n")

	// Task rows.
	if len(a.rows) == 0 {
		b.WriteString(stDim.Render("  no tasks — press n to create one") + "\n")
	}
	for i, r := range a.rows {
		indent := strings.Repeat(" ", r.StageIdx*step)
		text := fmt.Sprintf("%s %s %s", r.ID, r.Prio, truncate(r.Title, 44))
		if i == a.cur {
			b.WriteString(" " + stAccent.Render("❯") + indent + stSel.Render(text) + "\n")
		} else {
			b.WriteString("  " + indent + text + "\n")
		}
	}

	// Prompt / confirm layers or status line.
	b.WriteString("\n")
	switch {
	case a.confirm != nil:
		b.WriteString(" " + a.confirm.text + "  " +
			stKey.Render("Enter") + " yes · " + stKey.Render("Esc") + " no\n")
	case a.prompt != nil:
		b.WriteString(a.prompt.view())
	default:
		if a.status != "" {
			b.WriteString(" " + a.status + "\n")
		} else if r, ok := a.selected(); ok {
			b.WriteString(" " + stDim.Render(fmt.Sprintf("%s · %s · %s", r.ID, r.Stage, r.Prio)) + "\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString(" " + stDim.Render("↑↓ task · Tab/⇧Tab stage · Enter open · n new · b backlog · B backlog view · S sprint · , roles · q quit") + "\n")
	}
	return b.String()
}

func baseName(p string) string {
	if i := lastSlash(p); i >= 0 {
		return p[i+1:]
	}
	return p
}
