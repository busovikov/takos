package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/busovikov/takos/internal/core"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "ws"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ws: ws, w: 100, h: 30}
	a.reload()
	return a
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func send(a *App, keys ...string) {
	for _, k := range keys {
		a.Update(key(k))
	}
}

func typeText(a *App, text string) {
	for _, r := range text {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestBoardTabMovesThroughValidator(t *testing.T) {
	a := newTestApp(t)
	task, _, err := a.ws.CreateTask("Board task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.reload()

	// Tab: refinement → planning.
	send(a, "tab")
	pos, _ := a.ws.FindHandler(task.Meta.ID)
	if pos.Stage != "planning" {
		t.Fatalf("after Tab: %s", pos.Stage)
	}

	// Tab again: blocked by the plan-resolved gate; stage unchanged.
	send(a, "tab")
	pos, _ = a.ws.FindHandler(task.Meta.ID)
	if pos.Stage != "planning" {
		t.Fatalf("gate did not hold: %s", pos.Stage)
	}
	if !strings.Contains(a.status, "plan is not resolved") {
		t.Errorf("status must explain the refusal, got %q", a.status)
	}

	// ⇧Tab: back to refinement.
	send(a, "shift+tab")
	pos, _ = a.ws.FindHandler(task.Meta.ID)
	if pos.Stage != "refinement" {
		t.Fatalf("after ⇧Tab: %s", pos.Stage)
	}
}

func TestCreateFlowTwoSteps(t *testing.T) {
	a := newTestApp(t)

	send(a, "n")
	if a.prompt == nil {
		t.Fatal("n must open the title prompt")
	}
	typeText(a, "New TUI task")
	send(a, "enter")

	// The task exists already; the second prompt asks for the Objective.
	if a.prompt == nil || !strings.Contains(a.prompt.label, "Objective") {
		t.Fatal("the Objective prompt must follow")
	}
	dir, err := a.ws.FindTaskDir("T-0001")
	if err != nil {
		t.Fatalf("task must exist after the first Enter: %v", err)
	}
	typeText(a, "Draft of the idea")
	send(a, "enter")
	if a.prompt != nil {
		t.Fatal("prompt must close")
	}
	task, _ := core.LoadTask(dir)
	if got := core.GetSection(task.Body, "Objective"); got != "Draft of the idea" {
		t.Errorf("Objective = %q", got)
	}

	// Esc on the title step creates nothing.
	send(a, "n")
	send(a, "esc")
	if _, err := a.ws.FindTaskDir("T-0002"); err == nil {
		t.Error("Esc on the title step must not create a task")
	}
}

func TestCardMenuAndBacklogConfirm(t *testing.T) {
	a := newTestApp(t)
	task, _, _ := a.ws.CreateTask("Card task", core.CreateOpts{})
	a.reload()

	send(a, "enter")
	if a.scr != scrCard {
		t.Fatal("Enter must open the card")
	}
	if !strings.Contains(a.View(), "ACTIONS") {
		t.Error("card view must render the menu")
	}

	// b → confirm → Enter sends to the backlog.
	send(a, "b")
	if a.confirm == nil {
		t.Fatal("b must ask for confirmation")
	}
	send(a, "enter")
	pos, _ := a.ws.FindHandler(task.Meta.ID)
	if pos.Area != core.AreaBacklog {
		t.Fatalf("after confirm: %+v", pos)
	}
	if a.scr != scrBoard {
		t.Error("must return to the board")
	}
}

func TestCarouselTriageAutoMove(t *testing.T) {
	a := newTestApp(t)
	task, _, _ := a.ws.CreateTask("Review task", core.CreateOpts{})
	id := task.Meta.ID
	a.ws.MoveTask(id, "planning", "user", "")
	core.SetCheck(task.Dir, "plan-resolved")
	a.ws.MoveTask(id, "implementation", "user", "")
	a.ws.MoveTask(id, "review", "user", "")
	core.AddComment(task.Dir, id, "First finding")
	core.AddComment(task.Dir, id, "Second finding")
	a.reload()

	a.openCard(id)
	send(a, "v")
	if a.scr != scrCarousel {
		t.Fatal("v must open the carousel")
	}

	// Triage: first rework, second resolved → verdict rework → auto-move.
	send(a, "w")
	if a.card.round.Items[0].Status != core.ReviewRework {
		t.Fatalf("R1 status: %s", a.card.round.Items[0].Status)
	}
	if a.car.idx != 1 {
		t.Fatalf("carousel must jump to the next open finding, idx=%d", a.car.idx)
	}
	send(a, "r")
	pos, _ := a.ws.FindHandler(id)
	if pos.Stage != "implementation" {
		t.Fatalf("verdict rework must move the task back, got %s", pos.Stage)
	}
	if a.scr != scrBoard {
		t.Error("after the verdict the board must be shown")
	}
}

func TestBacklogScreenTake(t *testing.T) {
	a := newTestApp(t)
	task, _, _ := a.ws.CreateTask("Backlog task", core.CreateOpts{Backlog: true})
	a.reload()

	send(a, "B")
	if a.scr != scrBacklog {
		t.Fatal("B must open the backlog screen")
	}
	send(a, "s")
	pos, _ := a.ws.FindHandler(task.Meta.ID)
	if pos.Sprint != "sprint-2" || pos.Stage != "refinement" {
		t.Fatalf("after s: %+v", pos)
	}
	// b returns it to the backlog.
	send(a, "b")
	pos, _ = a.ws.FindHandler(task.Meta.ID)
	if pos.Area != core.AreaBacklog {
		t.Fatalf("after b: %+v", pos)
	}
}

func TestSprintScreenClose(t *testing.T) {
	a := newTestApp(t)
	send(a, "S")
	if a.scr != scrSprint {
		t.Fatal("S must open the sprint screen")
	}
	send(a, "c")
	if a.confirm == nil {
		t.Fatal("close must ask for confirmation")
	}
	send(a, "enter")
	active, _ := a.ws.ActiveSprint()
	if active.Name != "sprint-2" {
		t.Fatalf("after close the active sprint is %s", active.Name)
	}
}

func TestViewsRenderAtEveryScreen(t *testing.T) {
	a := newTestApp(t)
	a.ws.CreateTask("Render me", core.CreateOpts{})
	a.reload()

	if v := a.View(); !strings.Contains(v, "T-0001") || !strings.Contains(v, "refinement") {
		t.Errorf("board view: %q", v)
	}
	a.openCard("T-0001")
	if v := a.View(); !strings.Contains(v, "Render me") {
		t.Errorf("card view: %q", v)
	}
	a.scr = scrBoard
	send(a, "B")
	_ = a.View()
	send(a, "esc", "S")
	_ = a.View()
	send(a, "esc", ",")
	if v := a.View(); !strings.Contains(v, "ROLE SETTINGS") {
		t.Errorf("roles view: %q", v)
	}
}
