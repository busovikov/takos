package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
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
	// A process that died without a terminal marker record (the daemon
	// hasn't scanned it yet, or nobody's running one at all) would
	// otherwise look "running" forever — the card would show a stale
	// interrupt action for a pid that's long gone.
	session.RepairStale(dir)

	// The periodic refresh (spinner, live elapsed time) reopens the same
	// card every second, rebuilding the menu from scratch — keep the
	// cursor on the action the user had highlighted instead of snapping
	// back to the top on every tick.
	var selKey string
	if a.card != nil && a.card.id == id && a.card.sel < len(a.card.menu) {
		selKey = a.card.menu[a.card.sel].key
	}

	c := &cardState{id: id, task: task, pos: pos}
	c.resolved = core.HasCheck(dir, "plan-resolved")
	if fi, err := os.Stat(filepath.Join(dir, core.PlanFile)); err == nil {
		c.planSize = fi.Size()
	}
	c.round, c.roundPath, _ = core.LoadLatestRound(dir)
	a.card = c
	a.buildCardMenu()
	if selKey != "" {
		for i, m := range a.card.menu {
			if m.key == selKey {
				a.card.sel = i
				break
			}
		}
	}
	a.scr = scrCard
}

func (a *App) buildCardMenu() {
	c := a.card
	var m []menuItem

	// A task in a non-active sprint (e.g. "next") isn't on the active
	// staircase — the backend refuses moves and session starts for it, so
	// don't offer them either.
	sprintActive := true
	if c.pos.Area == core.AreaSprint {
		active, err := a.ws.ActiveSprint()
		sprintActive = err == nil && c.pos.Sprint == active.Name
	}

	// Agent session actions: contextual per the entry policy; the
	// recommended action goes first.
	if sprintActive && c.pos.Area != core.AreaArchive && c.pos.Stage != core.Backlog && c.pos.Stage != "done" {
		if active := session.Active(c.task.Dir); active != nil {
			name, pid := active.Name, active.PID
			label := name
			if isHandoffDigest(name) {
				label = "hand-off digest prep"
			}
			m = append(m, menuItem{"x", fmt.Sprintf("interrupt session %s (pid %d)", label, pid),
				func(a *App) tea.Cmd {
					a.confirm = &confirmState{
						text: fmt.Sprintf("interrupt session %s?", label),
						yes: func(a *App) tea.Cmd {
							if _, err := session.Cancel(c.task.Dir); err != nil {
								return a.flash(stErr, "✗ %v", err)
							}
							a.openCard(c.id)
							return a.flash(stOK, "✔ SIGTERM → %s", label)
						},
					}
					return nil
				}})
		} else if events, err := core.ReadHistory(c.task.Dir); err == nil {
			id := c.id
			plan := session.PlanEntry(events, c.id, c.pos.Stage)
			switch {
			case plan.Mode == session.ModeResume:
				m = append(m, menuItem{"a", fmt.Sprintf("continue session %s (limit → resume)", plan.ResumeName),
					func(a *App) tea.Cmd { return a.buildSession(id, true, false) }})
			case plan.ResumeName != "":
				// A prior session lives in this stage (interrupted, or a clean
				// finish) — offer continue vs. a fresh generation. Available
				// in every stage; continue is the recommended first item.
				m = append(m, menuItem{"a", fmt.Sprintf("continue session %s", plan.ResumeName),
					func(a *App) tea.Cmd { return a.buildSession(id, true, false) }})
				m = append(m, menuItem{"n", fmt.Sprintf("new session #%d instead", plan.NewGen),
					func(a *App) tea.Cmd { return a.buildSession(id, false, true) }})
			default:
				role := session.RoleForStage(c.pos.Stage)
				if role == "" {
					role = "agent"
				}
				m = append(m, menuItem{"a", fmt.Sprintf("%s session (new #%d)", role, plan.NewGen),
					func(a *App) tea.Cmd { return a.buildSession(id, false, false) }})
			}
		}
	}

	if c.pos.Stage == "review" {
		if c.round != nil {
			open := c.round.Counts()[core.ReviewOpen]
			m = append(m, menuItem{"v", fmt.Sprintf("findings (%d, %d open)", len(c.round.Items), open),
				func(a *App) tea.Cmd { a.openCarousel(); return nil }})
		}
		m = append(m, menuItem{"c", "add a finding", func(a *App) tea.Cmd {
			id := c.id
			a.prompt = newAreaPrompt(id+" · new finding:",
				"Enter — add to the current round · Ctrl+J — new line · Esc — cancel",
				func(a *App, text string) tea.Cmd {
					if text == "" {
						return nil
					}
					itemID, err := a.ws.AddReviewComment(id, text)
					if err != nil {
						return a.flash(stErr, "✗ %v", err)
					}
					a.openCard(id)
					return a.flash(stOK, "✔ finding %s added", itemID)
				})
			return nil
		}})
	}

	if c.pos.Stage == "planning" && !c.resolved && c.planSize > 0 {
		m = append(m, menuItem{"r", "resolve the plan (opens implementation)", func(a *App) tea.Cmd {
			core.SetCheck(c.task.Dir, "plan-resolved")
			core.AppendHistory(c.task.Dir, "user", "check", "plan-resolved")
			a.openCard(c.id)
			return a.flash(stOK, "✔ %s: plan resolved", c.id)
		}})
	}

	if next, ok := a.ws.NextStage(c.pos.Stage); sprintActive && ok {
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

	m = append(m, menuItem{"e", editorLabel(core.PlanFile), editorCmd(c.task.Dir, core.PlanFile)})
	m = append(m, menuItem{"t", editorLabel(core.TaskFile), editorCmd(c.task.Dir, core.TaskFile)})

	if c.pos.Area != core.AreaArchive && c.pos.Stage != core.Backlog && c.pos.Stage != "done" {
		m = append(m, menuItem{"b", "send to the backlog", func(a *App) tea.Cmd {
			id, title := c.id, c.task.Meta.Title
			a.confirm = &confirmState{
				text: fmt.Sprintf("send %s %q to the backlog?", id, title),
				yes: func(a *App) tea.Cmd {
					res, err := a.ws.MoveTask(id, core.Backlog, "user", "")
					if err != nil {
						a.reload()
						return a.flash(stErr, "✗ %v", err)
					}
					a.leaveCard()
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

// editorLabel names the action: an overridden $EDITOR is worth spelling out,
// otherwise the file simply goes to the system default application.
func editorLabel(file string) string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return file + " in " + filepath.Base(editor)
	}
	return file
}

func editorCmd(dir, file string) func(*App) tea.Cmd {
	return func(a *App) tea.Cmd {
		path := filepath.Join(dir, file)
		if editor := os.Getenv("EDITOR"); editor != "" {
			cmd := exec.Command(editor, path)
			return tea.ExecProcess(cmd, func(error) tea.Msg { return editorDoneMsg{} })
		}
		// The system opener detaches into a separate application, so the TUI
		// keeps running instead of waiting on a child process.
		if err := core.OpenWithSystem(path).Start(); err != nil {
			return a.flash(stErr, "✗ %v", err)
		}
		return a.flash(stOK, "✔ %s opened", file)
	}
}

// leaveCard closes the card and returns to whichever screen opened it
// (board or backlog), refreshing that screen's rows.
func (a *App) leaveCard() {
	a.scr = a.cardFrom
	if a.scr == scrBacklog && a.bl != nil {
		a.refreshBacklog()
	} else {
		a.scr = scrBoard
		a.reload()
	}
	a.cardFrom = scrBoard
}

func (a *App) updateCard(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	c := a.card
	switch key.String() {
	case "esc", "q":
		a.leaveCard()
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
	if line := sessionLine(c.task.Dir, a.spinner()); line != "" {
		fmt.Fprintf(&b, " %s %s\n", stDim.Render("SESSION"), line)
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
