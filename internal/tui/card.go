package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
)

// The task card: sections on top, a contextual action menu with a cursor and
// hotkeys below. Unavailable actions are absent, not dimmed.

type cardState struct {
	id        string
	task      *core.Task
	pos       *core.HandlerPos
	round     *core.ReviewRound
	roundPath string
	resolved  bool
	planSize  int64
	menu      []menuItem
	sel       int
}

type menuItem struct {
	key   string
	label string
	run   func(a *App) tea.Cmd
}

func (a *App) openCard(id string) {
	dir, err := a.ws.FindTaskDir(id)
	if err != nil {
		return
	}
	task, err := core.LoadTask(dir)
	if err != nil {
		return
	}
	pos, err := a.ws.FindHandler(id)
	if err != nil {
		return
	}
	c := &cardState{id: id, task: task, pos: pos}
	c.resolved = core.HasCheck(dir, "plan-resolved")
	if fi, err := os.Stat(filepath.Join(dir, core.PlanFile)); err == nil {
		c.planSize = fi.Size()
	}
	c.round, c.roundPath, _ = core.LoadLatestRound(dir)
	a.card = c
	a.buildCardMenu()
	a.scr = scrCard
}

func (a *App) buildCardMenu() {
	c := a.card
	var m []menuItem

	if c.round != nil {
		open := c.round.Counts()[core.ReviewOpen]
		m = append(m, menuItem{"v", fmt.Sprintf("findings (%d, %d open)", len(c.round.Items), open),
			func(a *App) tea.Cmd { a.openCarousel(); return nil }})
	}
	m = append(m, menuItem{"c", "add a finding", func(a *App) tea.Cmd {
		id, dir := c.id, c.task.Dir
		a.prompt = newAreaPrompt(id+" · new finding:",
			"Enter — add to the current round · Ctrl+J — new line · Esc — cancel",
			func(a *App, text string) tea.Cmd {
				if text == "" {
					return nil
				}
				itemID, err := core.AddComment(dir, id, text)
				if err != nil {
					return a.flash(stErr, "✗ %v", err)
				}
				a.openCard(id)
				return a.flash(stOK, "✔ finding %s added", itemID)
			})
		return nil
	}})

	if c.pos.Stage == "planning" && !c.resolved && c.planSize > 0 {
		m = append(m, menuItem{"r", "resolve the plan (opens implementation)", func(a *App) tea.Cmd {
			core.SetCheck(c.task.Dir, "plan-resolved")
			core.AppendHistory(c.task.Dir, "user", "check", "plan-resolved")
			a.openCard(c.id)
			return a.flash(stOK, "✔ %s: plan resolved", c.id)
		}})
	}

	if next, ok := a.ws.NextStage(c.pos.Stage); ok {
		m = append(m, menuItem{"→", "move to " + next, func(a *App) tea.Cmd {
			res, err := a.ws.MoveTask(c.id, next, "user", "")
			a.reload()
			if err != nil {
				return a.flash(stErr, "✗ %v", err)
			}
			a.openCard(c.id)
			return a.flash(stOK, "✔ %s: %s → %s", res.ID, res.From, res.To)
		}})
	}

	if c.planSize > 0 {
		m = append(m, menuItem{"e", "plan.md in $EDITOR", editorCmd(c.task.Dir, core.PlanFile)})
	}
	m = append(m, menuItem{"t", "task.md in $EDITOR", editorCmd(c.task.Dir, core.TaskFile)})

	if c.pos.Area != core.AreaArchive && c.pos.Stage != core.Backlog {
		m = append(m, menuItem{"b", "send to the backlog", func(a *App) tea.Cmd {
			id, title := c.id, c.task.Meta.Title
			a.confirm = &confirmState{
				text: fmt.Sprintf("send %s %q to the backlog?", id, title),
				yes: func(a *App) tea.Cmd {
					res, err := a.ws.MoveTask(id, core.Backlog, "user", "")
					a.reload()
					if err != nil {
						return a.flash(stErr, "✗ %v", err)
					}
					a.scr = scrBoard
					return a.flash(stOK, "✔ %s → backlog", res.ID)
				},
			}
			return nil
		}})
	}
	c.menu = m
	if c.sel >= len(m) {
		c.sel = 0
	}
}

func editorCmd(dir, file string) func(*App) tea.Cmd {
	return func(a *App) tea.Cmd {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			return a.flash(stErr, "✗ $EDITOR is not set")
		}
		cmd := exec.Command(editor, filepath.Join(dir, file))
		return tea.ExecProcess(cmd, func(error) tea.Msg { return editorDoneMsg{} })
	}
}

func (a *App) updateCard(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	c := a.card
	switch key.String() {
	case "esc", "q":
		a.scr = scrBoard
		a.reload()
		return a, nil
	case "up", "k":
		if c.sel > 0 {
			c.sel--
		}
		return a, nil
	case "down", "j":
		if c.sel < len(c.menu)-1 {
			c.sel++
		}
		return a, nil
	case "enter":
		if c.sel < len(c.menu) {
			return a, c.menu[c.sel].run(a)
		}
		return a, nil
	}
	// Hotkeys fire regardless of the cursor.
	for _, item := range c.menu {
		if key.String() == item.key || (item.key == "→" && key.String() == "right") {
			return a, item.run(a)
		}
	}
	return a, nil
}

func (a *App) viewCard() string {
	c := a.card
	var b strings.Builder
	where := c.pos.Stage
	if c.pos.Sprint != "" {
		where = c.pos.Sprint + " · " + c.pos.Stage
	}
	fmt.Fprintf(&b, " %s · %s%s\n",
		stTitle.Render(c.id), stTitle.Render(truncate(c.task.Meta.Title, 50)),
		stDim.Render("    "+where+" · "+c.task.Meta.Priority))
	fmt.Fprintf(&b, " %s\n\n", stDim.Render("repo: "+orDash(c.task.Meta.Repo)+" · automation: "+c.task.Meta.Automation))

	for _, sec := range []string{"Objective", "Context", "What is done", "Docs", "Testing"} {
		text := core.GetSection(c.task.Body, sec)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, " %s\n", stDim.Render(strings.ToUpper(sec)))
		for _, line := range strings.Split(truncateLines(text, 4), "\n") {
			fmt.Fprintf(&b, "   %s\n", truncate(line, max(a.w-4, 40)))
		}
	}

	// Plan / review state line.
	var facts []string
	if c.planSize > 0 {
		state := "not resolved"
		if c.resolved {
			state = stOK.Render("resolved ✔")
		}
		facts = append(facts, fmt.Sprintf("plan.md · %s", state))
	}
	if c.round != nil {
		cnt := c.round.Counts()
		facts = append(facts, fmt.Sprintf("%s · %d open", filepath.Base(c.roundPath), cnt[core.ReviewOpen]))
	}
	if len(facts) > 0 {
		fmt.Fprintf(&b, "\n %s %s\n", stDim.Render("FILES"), strings.Join(facts, " · "))
	}

	// History tail.
	if events, _ := core.ReadHistory(c.task.Dir); len(events) > 0 {
		fmt.Fprintf(&b, " %s\n", stDim.Render("HISTORY"))
		from := max(0, len(events)-3)
		for _, e := range events[from:] {
			fmt.Fprintf(&b, "   %s\n", stDim.Render(fmt.Sprintf("%s [%s] %s %s",
				e.At.Local().Format("02.01 15:04"), e.Actor, e.Verb, truncate(e.Args, 40))))
		}
	}

	// Action menu.
	b.WriteString("\n " + stDim.Render("ACTIONS") + "\n")
	for i, item := range c.menu {
		cursor := "  "
		label := item.label
		if i == c.sel {
			cursor = " " + stAccent.Render("❯")
			label = stSel.Render(label)
		}
		fmt.Fprintf(&b, "%s %s  %s\n", cursor, stKey.Render(item.key), label)
	}

	b.WriteString("\n")
	switch {
	case a.confirm != nil:
		b.WriteString(" " + a.confirm.text + "  " + stKey.Render("Enter") + " yes · " + stKey.Render("Esc") + " no\n")
	case a.prompt != nil:
		b.WriteString(a.prompt.view())
	default:
		if a.status != "" {
			b.WriteString(" " + a.status + "\n")
		}
		b.WriteString(" " + stDim.Render("↑↓ select · Enter run · hotkey fires directly · Esc back") + "\n")
	}
	return b.String()
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
