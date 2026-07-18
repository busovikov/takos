package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
)

// The review carousel: one finding per screen; r/w/t triage it, Tab/⇧Tab
// cycle endlessly, and after a triage the cursor jumps to the next open
// finding. When the last open one is handled the verdict moves the task.

type carState struct {
	idx int
}

func (a *App) openCarousel() {
	if a.card == nil || a.card.round == nil || len(a.card.round.Items) == 0 {
		return
	}
	a.car = &carState{}
	// Start at the first open finding, if any.
	for i, it := range a.card.round.Items {
		if it.Status == core.ReviewOpen {
			a.car.idx = i
			break
		}
	}
	a.scr = scrCarousel
}

func (a *App) updateCarousel(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return a, nil
	}
	c, car := a.card, a.car
	n := len(c.round.Items)
	switch key.String() {
	case "esc", "q":
		a.openCard(c.id) // re-read state
		return a, nil
	case "tab", "right", "l":
		car.idx = (car.idx + 1) % n
	case "shift+tab", "left", "h":
		car.idx = (car.idx - 1 + n) % n
	case "r":
		return a, a.triage(core.ReviewResolved)
	case "w":
		return a, a.triage(core.ReviewRework)
	case "t":
		return a, a.triage(core.ReviewTechDebt)
	}
	return a, nil
}

func (a *App) triage(st core.ReviewStatus) tea.Cmd {
	c, car := a.card, a.car
	item := c.round.Items[car.idx]
	if err := c.round.SetStatus(item.ID, st); err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	if err := core.SaveRound(c.roundPath, c.round); err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	core.AppendHistory(c.task.Dir, "user", "triage",
		fmt.Sprintf("%s %s -> %s", filepath.Base(c.roundPath), item.ID, st))

	var cmds []tea.Cmd
	cmds = append(cmds, a.flash(stOK, "✔ %s → %s", item.ID, st))

	// Tech debt spawns a backlog task pointing at its source.
	if st == core.ReviewTechDebt {
		debt, _, err := a.ws.CreateTask(item.Title, core.CreateOpts{
			Objective: fmt.Sprintf("%s\n\nsource: %s · %s#%s",
				item.Body, c.id, filepath.Base(c.roundPath), item.ID),
			Repo: c.task.Meta.Repo, Backlog: true,
		})
		if err == nil {
			cmds = []tea.Cmd{a.flash(stOK, "✔ %s → tech-debt · %s created in the backlog", item.ID, debt.Meta.ID)}
		}
	}

	// Triage finished → verdict and auto-move.
	if v, done := c.round.Verdict(); done {
		target := "done"
		if v == "rework" {
			target = "implementation"
		}
		id := c.id
		if res, err := a.ws.MoveTask(id, target, "engine", ""); err == nil {
			a.reload()
			a.scr = scrBoard
			return a.flash(stOK, "✔ round triaged · %s → %s", res.ID, res.To)
		}
		a.openCard(id)
		return tea.Batch(cmds...)
	}

	// Jump to the next open finding.
	n := len(c.round.Items)
	for i := 1; i <= n; i++ {
		j := (car.idx + i) % n
		if c.round.Items[j].Status == core.ReviewOpen {
			car.idx = j
			break
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) viewCarousel() string {
	c, car := a.card, a.car
	item := c.round.Items[car.idx]
	cnt := c.round.Counts()

	var b strings.Builder
	fmt.Fprintf(&b, " %s · %s · finding %d/%d%s\n\n",
		stTitle.Render(c.id), filepath.Base(c.roundPath), car.idx+1, len(c.round.Items),
		stDim.Render(fmt.Sprintf("   %d open · %d resolved · %d rework · %d tech-debt",
			cnt[core.ReviewOpen], cnt[core.ReviewResolved], cnt[core.ReviewRework], cnt[core.ReviewTechDebt])))

	status := string(item.Status)
	switch item.Status {
	case core.ReviewOpen:
		status = stErr.Render(status)
	case core.ReviewResolved:
		status = stOK.Render(status)
	default:
		status = stAccent.Render(status)
	}
	fmt.Fprintf(&b, " %s %s — %s\n\n", stKey.Render("["+item.ID+"]"), stTitle.Render(item.Title), status)

	for _, line := range strings.Split(item.Body, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			b.WriteString("   " + stDiffAdd.Render(line) + "\n")
		case strings.HasPrefix(line, "-"):
			b.WriteString("   " + stDiffDel.Render(line) + "\n")
		default:
			b.WriteString("   " + truncate(line, max(a.w-4, 40)) + "\n")
		}
	}

	b.WriteString("\n " + stKey.Render("r") + " resolve · " + stKey.Render("w") + " rework · " +
		stKey.Render("t") + " tech-debt\n")
	if a.status != "" {
		b.WriteString(" " + a.status + "\n")
	}
	b.WriteString(" " + stDim.Render("Tab next · ⇧Tab previous (loops) · Esc back to the card") + "\n")
	return b.String()
}
