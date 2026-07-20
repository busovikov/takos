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

	res, err := a.ws.TriageFinding(c.id, item.ID, st)
	if err != nil {
		return a.flash(stErr, "✗ %v", err)
	}
	// Re-read the round so the carousel reflects the persisted statuses.
	if round, path, err := core.LoadLatestRound(c.task.Dir); err == nil && round != nil {
		c.round, c.roundPath = round, path
	}

	msg := fmt.Sprintf("✔ %s → %s", res.ItemID, res.Status)
	if res.DebtID != "" {
		msg = fmt.Sprintf("✔ %s → tech-debt · %s created in the backlog", res.ItemID, res.DebtID)
	}

	// A fully triaged round auto-moved the task — back to the board.
	if res.Done {
		if res.MovedTo != "" {
			a.reload()
			a.scr = scrBoard
			return a.flash(stOK, "✔ round triaged · %s → %s", c.id, res.MovedTo)
		}
		a.openCard(c.id)
		return a.flash(stOK, "%s", msg)
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
	return a.flash(stOK, "%s", msg)
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
