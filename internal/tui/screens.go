package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/agents"
	"github.com/busovikov/takos/internal/core"
)

// --- Backlog screen: the next-sprint section first, then the backlog. ---

type backlogState struct {
	next    []row
	backlog []row
	cur     int
}

func (a *App) openBacklog() {
	a.bl = &backlogState{}
	a.refreshBacklog()
	a.scr = scrBacklog
}

func (a *App) refreshBacklog() {
	bl := a.bl
	bl.next, bl.backlog = nil, nil
	nextSprint, _ := a.ws.NextSprint()
	handlers, err := a.ws.ListHandlers("")
	if err != nil {
		return
	}
	for _, h := range handlers {
		r, ok := a.rowFor(h)
		if !ok {
			continue
		}
		switch {
		case h.Area == core.AreaBacklog:
			bl.backlog = append(bl.backlog, r)
		case nextSprint != nil && h.Sprint == nextSprint.Name:
			bl.next = append(bl.next, r)
		}
	}
	if bl.cur >= len(bl.next)+len(bl.backlog) {
		bl.cur = max(0, len(bl.next)+len(bl.backlog)-1)
	}
}

func (a *App) blSelected() (row, bool) {
	bl := a.bl
	all := append(append([]row{}, bl.next...), bl.backlog...)
	if bl.cur < 0 || bl.cur >= len(all) {
		return row{}, false
	}
	return all[bl.cur], true
}

func (a *App) updateBacklog(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	bl := a.bl
	total := len(bl.next) + len(bl.backlog)
	switch key.String() {
	case "esc", "q":
		a.scr = scrBoard
		a.reload()
	case "up", "k":
		if bl.cur > 0 {
			bl.cur--
		}
	case "down", "j":
		if bl.cur < total-1 {
			bl.cur++
		}
	case "enter":
		if r, ok := a.blSelected(); ok {
			a.cardFrom = scrBacklog
			a.openCard(r.ID)
		}
	case "n":
		return a, a.startCreatePrompt(true)
	case "s":
		if r, ok := a.blSelected(); ok && r.Area == core.AreaBacklog {
			first := a.ws.WF.Stages[0].ID
			res, err := a.ws.MoveTask(r.ID, first, "user", "next")
			a.refreshBacklog()
			if err != nil {
				return a, a.flash(stErr, "✗ %v", err)
			}
			return a, a.flash(stOK, "✔ %s → next sprint (%s)", res.ID, res.To)
		}
	case "b":
		if r, ok := a.blSelected(); ok && r.Area != core.AreaBacklog {
			res, err := a.ws.MoveTask(r.ID, core.Backlog, "user", "")
			a.refreshBacklog()
			if err != nil {
				return a, a.flash(stErr, "✗ %v", err)
			}
			return a, a.flash(stOK, "✔ %s → backlog", res.ID)
		}
	}
	return a, nil
}

func (a *App) viewBacklog() string {
	bl := a.bl
	var b strings.Builder
	b.WriteString(" " + stTitle.Render("BACKLOG") + "\n\n")

	nextSprint, _ := a.ws.NextSprint()
	idx := 0
	if nextSprint != nil {
		b.WriteString(" " + stDim.Render("NEXT SPRINT · "+nextSprint.Name) + "\n")
		if len(bl.next) == 0 {
			b.WriteString(stDim.Render("   empty") + "\n")
		}
		for _, r := range bl.next {
			b.WriteString(a.blRow(r, idx))
			idx++
		}
		b.WriteString("\n")
	}
	b.WriteString(" " + stDim.Render(fmt.Sprintf("BACKLOG · %d", len(bl.backlog))) + "\n")
	if len(bl.backlog) == 0 {
		b.WriteString(stDim.Render("   empty") + "\n")
	}
	for _, r := range bl.backlog {
		b.WriteString(a.blRow(r, idx))
		idx++
	}

	b.WriteString("\n")
	switch {
	case a.prompt != nil:
		b.WriteString(a.prompt.view())
	default:
		if a.status != "" {
			b.WriteString(" " + a.status + "\n")
		} else if r, ok := a.blSelected(); ok {
			b.WriteString(" " + stDim.Render(r.ID+" · "+r.Prio) + "\n")
		}
		b.WriteString(" " + stDim.Render("↑↓ select · Enter open · s → next sprint · b → backlog · n new · Esc board") + "\n")
	}
	return b.String()
}

func (a *App) blRow(r row, idx int) string {
	text := fmt.Sprintf("%s  %s  %s", r.ID, r.Prio, truncate(r.Title, 44))
	if idx == a.bl.cur {
		return " " + stAccent.Render("❯") + " " + stSel.Render(text) + "\n"
	}
	return "   " + text + "\n"
}

// --- Sprint screen: status, manual close, sprint.md editing. ---

type sprintState struct {
	sel int
}

func (a *App) openSprint() {
	a.sp = &sprintState{}
	a.scr = scrSprint
}

func (a *App) updateSprint(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	switch key.String() {
	case "esc", "q":
		a.scr = scrBoard
		a.reload()
	case "c":
		active, err := a.ws.ActiveSprint()
		if err != nil {
			return a, a.flash(stErr, "✗ %v", err)
		}
		a.confirm = &confirmState{
			text: fmt.Sprintf("close %s now? done → archive, unfinished → next sprint", active.Name),
			yes: func(a *App) tea.Cmd {
				closed, promoted, carried, err := a.ws.CloseActiveSprint(time.Now())
				a.reload()
				if err != nil {
					return a.flash(stErr, "✗ %v", err)
				}
				a.scr = scrBoard
				return a.flash(stOK, "✔ %s closed · %s active · %d carried", closed, promoted, carried)
			},
		}
	case "e":
		if active, err := a.ws.ActiveSprint(); err == nil {
			return a, editorCmd(active.Dir, core.SprintFile)(a)
		}
	}
	return a, nil
}

func (a *App) viewSprint() string {
	var b strings.Builder
	active, err := a.ws.ActiveSprint()
	if err != nil {
		return " no active sprint\n"
	}
	next, _ := a.ws.NextSprint()
	day := int(time.Since(active.Meta.StartedAt).Hours()/24) + 1
	fmt.Fprintf(&b, " %s %s\n", stTitle.Render("SPRINT "+active.Name), stOK.Render("· active"))
	fmt.Fprintf(&b, " started %s · day %d of %d · closes %s automatically\n",
		active.Meta.StartedAt.Local().Format("02.01"), day, active.Meta.LengthDays,
		active.Meta.EndsAt.Local().Format("02.01"))
	fmt.Fprintf(&b, " tasks in sprint: %d", len(a.rows))
	if next != nil {
		fmt.Fprintf(&b, " · next: %s", next.Name)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, " %s %s\n", stKey.Render("c"), "close the sprint now (done → archive, unfinished → next)")
	fmt.Fprintf(&b, " %s %s\n", stKey.Render("e"), editorLabel(core.SprintFile)+" (dates, length)")

	b.WriteString("\n")
	if a.confirm != nil {
		b.WriteString(" " + a.confirm.text + "  " + stKey.Render("Enter") + " yes · " + stKey.Render("Esc") + " no\n")
	} else {
		if a.status != "" {
			b.WriteString(" " + a.status + "\n")
		}
		b.WriteString(" " + stDim.Render("Esc — back to the board") + "\n")
	}
	return b.String()
}

// --- Roles screen: assign detected agents/models to workflow roles. ---

type rolesState struct {
	names    []string
	sel      int
	detected []agents.Info
	// picking state: 0 — roles list, 1 — agent pick, 2 — model pick
	step      int
	pickIdx   int
	pickAgent agents.Info
}

func (a *App) openRoles() {
	names := sortedRoleNames(a.ws.WF.Roles)
	a.rl = &rolesState{names: names, detected: agents.Detect()}
	a.scr = scrRoles
}

func sortedRoleNames(roles map[string]core.Role) []string {
	order := []string{"context", "planner", "executor", "handover", "reviewer", "summarizer"}
	var out []string
	seen := map[string]bool{}
	for _, n := range order {
		if _, ok := roles[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	for n := range roles {
		if !seen[n] {
			out = append(out, n)
		}
	}
	return out
}

func (a *App) updateRoles(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	rl := a.rl
	switch rl.step {
	case 0: // roles list
		switch key.String() {
		case "esc", "q":
			a.scr = scrBoard
		case "up", "k":
			if rl.sel > 0 {
				rl.sel--
			}
		case "down", "j":
			if rl.sel < len(rl.names)-1 {
				rl.sel++
			}
		case "enter":
			rl.step, rl.pickIdx = 1, 0
		}
	case 1: // agent pick
		switch key.String() {
		case "esc":
			rl.step = 0
		case "up", "k":
			if rl.pickIdx > 0 {
				rl.pickIdx--
			}
		case "down", "j":
			if rl.pickIdx < len(rl.detected)-1 {
				rl.pickIdx++
			}
		case "enter":
			info := rl.detected[rl.pickIdx]
			if !info.Available {
				return a, a.flash(stErr, "✗ %s is not installed (%s)", info.ID, info.Install)
			}
			rl.pickAgent = info
			if len(info.Models) == 0 {
				return a, a.assignRole(rl.names[rl.sel], info.ID, "")
			}
			rl.step, rl.pickIdx = 2, 0
		}
	case 2: // model pick
		switch key.String() {
		case "esc":
			rl.step = 1
		case "up", "k":
			if rl.pickIdx > 0 {
				rl.pickIdx--
			}
		case "down", "j":
			if rl.pickIdx < len(rl.pickAgent.Models) {
				rl.pickIdx++
			}
		case "enter":
			model := "" // the last entry is "keep default"
			if rl.pickIdx < len(rl.pickAgent.Models) {
				model = rl.pickAgent.Models[rl.pickIdx]
			}
			return a, a.assignRole(rl.names[rl.sel], rl.pickAgent.ID, model)
		}
	}
	return a, nil
}

func (a *App) assignRole(role, agent, model string) tea.Cmd {
	if err := core.SetRoleAssignment(a.ws.Root, role, agent, model); err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	if ws, err := core.Open(a.ws.Root); err == nil {
		a.ws = ws
	}
	a.rl.step = 0
	if model != "" {
		return a.flash(stOK, "✔ %s → %s · %s (written to workflow.yaml)", role, agent, model)
	}
	return a.flash(stOK, "✔ %s → %s (written to workflow.yaml)", role, agent)
}

func (a *App) viewRoles() string {
	rl := a.rl
	var b strings.Builder
	b.WriteString(" " + stTitle.Render("ROLE SETTINGS") + stDim.Render("   workflow.yaml") + "\n\n")

	avail := map[string]bool{}
	for _, d := range rl.detected {
		avail[d.ID] = d.Available
	}

	switch rl.step {
	case 0:
		fmt.Fprintf(&b, " %s\n", stDim.Render(fmt.Sprintf("  %-12s %-13s %-26s", "ROLE", "AGENT", "MODEL")))
		for i, name := range rl.names {
			r := a.ws.WF.Roles[name]
			note := ""
			if !avail[r.Agent] {
				note = stErr.Render(" ⚠ not installed")
			}
			line := fmt.Sprintf("%-12s %-13s %-26s%s", name, r.Agent, orDash(r.Model), note)
			if i == rl.sel {
				b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(line) + "\n")
			} else {
				b.WriteString("   " + line + "\n")
			}
		}
		b.WriteString("\n " + stDim.Render("↑↓ role · Enter change · Esc back") + "\n")
	case 1:
		fmt.Fprintf(&b, " %s · agent:\n", rl.names[rl.sel])
		for i, d := range rl.detected {
			status := stOK.Render("✔ " + orDash(d.Version))
			if !d.Available {
				status = stDim.Render("✗ not installed")
			}
			line := fmt.Sprintf("%-13s %s", d.ID, status)
			if i == rl.pickIdx {
				b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(line) + "\n")
			} else {
				b.WriteString("   " + line + "\n")
			}
		}
		b.WriteString("\n " + stDim.Render("↑↓ · Enter select · Esc back") + "\n")
	case 2:
		fmt.Fprintf(&b, " %s · %s · model:\n", rl.names[rl.sel], rl.pickAgent.ID)
		for i, m := range rl.pickAgent.Models {
			if i == rl.pickIdx {
				b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(m) + "\n")
			} else {
				b.WriteString("   " + m + "\n")
			}
		}
		keep := "(keep the agent default)"
		if rl.pickIdx == len(rl.pickAgent.Models) {
			b.WriteString(" " + stAccent.Render("❯") + " " + stSel.Render(keep) + "\n")
		} else {
			b.WriteString("   " + stDim.Render(keep) + "\n")
		}
		b.WriteString("\n " + stDim.Render("↑↓ · Enter select · Esc back") + "\n")
	}
	if a.status != "" {
		b.WriteString(" " + a.status + "\n")
	}
	return b.String()
}
