package trackerd

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
)

func newTestDaemon(t *testing.T) (*Daemon, *core.Workspace) {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		ws:        ws,
		log:       log.New(os.Stderr, "", 0),
		startedAt: time.Now(),
	}
	d.snap = d.snapshot()
	return d, ws
}

// externalMv simulates a user's `mv` of a handler into another stage folder.
func externalMv(t *testing.T, ws *core.Workspace, pos *core.HandlerPos, stageDir string) string {
	t.Helper()
	active, err := ws.ActiveSprint()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(active.Dir, stageDir, filepath.Base(pos.Path))
	if err := os.Rename(pos.Path, dest); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestScanAcceptsValidExternalMove(t *testing.T) {
	d, ws := newTestDaemon(t)
	task, _, _ := ws.CreateTask("Moved by hand", core.CreateOpts{})
	d.snap = d.snapshot()

	pos, _ := ws.FindHandler(task.Meta.ID)
	externalMv(t, ws, pos, "2-planning") // refinement → planning is manual-allowed

	d.scan(false)

	got, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "planning" {
		t.Errorf("stage = %s, want planning", got.Stage)
	}
	// The accepted move is chronicled and the stage scaffold ran.
	stage, _ := core.LastMove(mustHistory(t, task.Dir))
	if stage != "planning" {
		t.Errorf("history stage = %s, want planning", stage)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, core.PlanFile)); err != nil {
		t.Error("plan.md was not scaffolded after the accepted mv")
	}
}

func TestScanRollsBackInvalidExternalMove(t *testing.T) {
	d, ws := newTestDaemon(t)
	task, _, _ := ws.CreateTask("Jumper", core.CreateOpts{})
	d.snap = d.snapshot()

	pos, _ := ws.FindHandler(task.Meta.ID)
	externalMv(t, ws, pos, "4-review") // refinement → review is not in the workflow

	d.scan(false)

	got, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "refinement" {
		t.Errorf("stage = %s, want refinement (rolled back)", got.Stage)
	}
	if _, err := os.Stat(filepath.Join(got.Path, core.TaskFile)); err != nil {
		t.Errorf("symlink broken after rollback: %v", err)
	}
	rejected := false
	for _, e := range mustHistory(t, task.Dir) {
		if e.Verb == "reject" && strings.Contains(e.Args, "review") {
			rejected = true
		}
	}
	if !rejected {
		t.Error("no reject event in history")
	}
}

func TestScanRollsBackGatedMove(t *testing.T) {
	d, ws := newTestDaemon(t)
	task, _, _ := ws.CreateTask("Gated", core.CreateOpts{})
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	d.snap = d.snapshot()

	// planning → implementation is allowed but gated by plan-resolved.
	pos, _ := ws.FindHandler(task.Meta.ID)
	externalMv(t, ws, pos, "3-implementation")
	d.scan(false)
	if got, _ := ws.FindHandler(task.Meta.ID); got.Stage != "planning" {
		t.Errorf("stage = %s, want planning (gate must hold for mv too)", got.Stage)
	}

	// With the check set the same gesture passes.
	core.SetCheck(task.Dir, "plan-resolved")
	d.snap = d.snapshot()
	pos, _ = ws.FindHandler(task.Meta.ID)
	externalMv(t, ws, pos, "3-implementation")
	d.scan(false)
	if got, _ := ws.FindHandler(task.Meta.ID); got.Stage != "implementation" {
		t.Errorf("stage = %s, want implementation", got.Stage)
	}
}

func TestScanAcceptsMoveToBacklog(t *testing.T) {
	d, ws := newTestDaemon(t)
	task, _, _ := ws.CreateTask("Back to the icebox", core.CreateOpts{})
	d.snap = d.snapshot()

	pos, _ := ws.FindHandler(task.Meta.ID)
	dest := filepath.Join(ws.BacklogDir(), filepath.Base(pos.Path))
	if err := os.Rename(pos.Path, dest); err != nil {
		t.Fatal(err)
	}
	d.scan(false)

	got, _ := ws.FindHandler(task.Meta.ID)
	if got.Area != core.AreaBacklog {
		t.Errorf("area = %s, want backlog", got.Area)
	}
	// The mv across depth levels broke the relative target — scan must have
	// repaired it.
	if _, err := os.Stat(filepath.Join(got.Path, core.TaskFile)); err != nil {
		t.Errorf("symlink not repaired after mv to backlog: %v", err)
	}
}

func TestScanSprintTickClosesOverdue(t *testing.T) {
	d, ws := newTestDaemon(t)
	active, _ := ws.ActiveSprint()
	active.Meta.EndsAt = time.Now().Add(-time.Hour)
	if err := core.SaveSprint(active.Dir, &active.Meta); err != nil {
		t.Fatal(err)
	}

	d.scan(true) // the poll tick runs the sprint scheduler

	got, err := ws.ActiveSprint()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name == active.Name {
		t.Error("overdue sprint not closed by the poll tick")
	}
}

func mustHistory(t *testing.T, dir string) []core.HistoryEvent {
	t.Helper()
	events, err := core.ReadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	return events
}
