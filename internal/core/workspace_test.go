package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestWS(t *testing.T) *Workspace {
	t.Helper()
	ws, err := Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestInitScaffold(t *testing.T) {
	ws := newTestWS(t)
	active, err := ws.ActiveSprint()
	if err != nil {
		t.Fatal(err)
	}
	next, err := ws.NextSprint()
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "sprint-1" || next.Name != "sprint-2" {
		t.Errorf("sprints: %s / %s", active.Name, next.Name)
	}
	if active.Meta.EndsAt.Sub(active.Meta.StartedAt) != 14*24*time.Hour {
		t.Errorf("sprint length: %v", active.Meta.EndsAt.Sub(active.Meta.StartedAt))
	}
	if _, err := os.Stat(filepath.Join(active.Dir, "3-implementation")); err != nil {
		t.Error("stage folder 3-implementation is missing")
	}
	// Idempotency.
	if _, err := Init(ws.Root); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestCreateTaskAndSymlink(t *testing.T) {
	ws := newTestWS(t)
	task, pos, err := ws.CreateTask("Add CSV report export", CreateOpts{Repo: "/tmp/r"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Meta.ID != "T-0001" {
		t.Errorf("id = %s", task.Meta.ID)
	}
	if pos.Stage != "refinement" || pos.Area != AreaSprint {
		t.Errorf("position: %+v", pos)
	}
	// The handler is a symlink resolving to the task folder.
	fi, err := os.Lstat(pos.Path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("handler is not a symlink: %v %v", fi, err)
	}
	resolved, err := filepath.EvalSymlinks(pos.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(task.Dir)
	if resolved != wantDir {
		t.Errorf("symlink points at %s, want %s", resolved, wantDir)
	}
	// task.md is readable through the symlink.
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("task.md unreachable through the symlink: %v", err)
	}

	// A second task goes to the backlog.
	_, pos2, err := ws.CreateTask("Dark dashboard theme", CreateOpts{Backlog: true})
	if err != nil {
		t.Fatal(err)
	}
	if pos2.Stage != Backlog {
		t.Errorf("backlog task position: %+v", pos2)
	}
}

func TestMoveHappyPathAndGates(t *testing.T) {
	ws := newTestWS(t)
	task, _, err := ws.CreateTask("Fix flaky auth test", CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id := task.Meta.ID

	// refinement → planning: scaffolds plan.md.
	if _, err := ws.MoveTask(id, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, PlanFile)); err != nil {
		t.Error("plan.md was not scaffolded on entering planning")
	}

	// planning → implementation is gated by plan-resolved.
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err == nil {
		t.Fatal("the transition must be closed by the plan-resolved gate")
	}
	pos, _ := ws.FindHandler(id)
	if pos.Stage != "planning" {
		t.Errorf("handler moved despite the refusal: %s", pos.Stage)
	}
	events, _ := ReadHistory(task.Dir)
	for _, e := range events {
		if e.Verb == "move" && strings.Contains(e.Args, "implementation") {
			t.Error("a refusal wrote a move into history")
		}
	}

	// resolve → the transition opens.
	if err := SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}

	// A transition absent from the whitelist.
	if _, err := ws.MoveTask(id, "done", "user", ""); err == nil {
		t.Fatal("implementation → done must not be allowed")
	}

	// implementation → review; review → done is closed for humans (manual: false).
	if _, err := ws.MoveTask(id, "review", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "done", "user", ""); err == nil {
		t.Fatal("review → done must be automation-only")
	}
	// …but automation may.
	if _, err := ws.MoveTask(id, "done", "engine", ""); err != nil {
		t.Fatal(err)
	}
	stage, _ := LastMove(mustHistory(t, task.Dir))
	if stage != "done" {
		t.Errorf("final stage per history: %s", stage)
	}
}

func TestOpenReviewBlocksMoves(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("Task with a review", CreateOpts{})
	id := task.Meta.ID
	ws.MoveTask(id, "planning", "user", "")
	SetCheck(task.Dir, "plan-resolved")
	ws.MoveTask(id, "implementation", "user", "")
	ws.MoveTask(id, "review", "user", "")

	if _, err := AddComment(task.Dir, id, "Export button visible for empty reports"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "done", "engine", ""); err == nil {
		t.Fatal("an open finding must block moves")
	}

	// Triage: resolving the only finding opens the way.
	round, path, err := LoadLatestRound(task.Dir)
	if err != nil || round == nil {
		t.Fatalf("round not found: %v", err)
	}
	round.SetStatus("R1", ReviewResolved)
	SaveRound(path, round)
	if v, done := round.Verdict(); !done || v != "done" {
		t.Errorf("verdict: %s %v", v, done)
	}
	if _, err := ws.MoveTask(id, "done", "engine", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBacklogTakeAndSend(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("From the backlog", CreateOpts{Backlog: true})
	id := task.Meta.ID

	// backlog → refinement into the NEXT sprint (take).
	if _, err := ws.MoveTask(id, "refinement", "user", "next"); err != nil {
		t.Fatal(err)
	}
	pos, _ := ws.FindHandler(id)
	if pos.Sprint != "sprint-2" || pos.Stage != "refinement" {
		t.Errorf("after take: %+v", pos)
	}
	// The symlink is valid at the new depth.
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("symlink broken after take: %v", err)
	}

	// Back to the backlog (b).
	if _, err := ws.MoveTask(id, Backlog, "user", ""); err != nil {
		t.Fatal(err)
	}
	pos, _ = ws.FindHandler(id)
	if pos.Area != AreaBacklog {
		t.Errorf("after b: %+v", pos)
	}
}

func TestCloseActiveSprint(t *testing.T) {
	ws := newTestWS(t)
	doneTask, _, _ := ws.CreateTask("Finished one", CreateOpts{})
	wip, _, _ := ws.CreateTask("Unfinished one", CreateOpts{})

	// Drive the first task to done via automation.
	ws.MoveTask(doneTask.Meta.ID, "planning", "user", "")
	SetCheck(doneTask.Dir, "plan-resolved")
	ws.MoveTask(doneTask.Meta.ID, "implementation", "user", "")
	ws.MoveTask(doneTask.Meta.ID, "review", "user", "")
	ws.MoveTask(doneTask.Meta.ID, "done", "engine", "")
	// The unfinished one — to planning.
	ws.MoveTask(wip.Meta.ID, "planning", "user", "")

	closed, promoted, carried, err := ws.CloseActiveSprint(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if closed != "sprint-1" || promoted != "sprint-2" || carried != 1 {
		t.Errorf("close: %s %s carried=%d", closed, promoted, carried)
	}

	// The done task is archived, stage preserved, symlink alive.
	pos, err := ws.FindHandler(doneTask.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pos.Area != AreaArchive || pos.Stage != "done" {
		t.Errorf("done task: %+v", pos)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("archived symlink broken: %v", err)
	}

	// The unfinished one is in the new active sprint, same stage.
	pos, _ = ws.FindHandler(wip.Meta.ID)
	if pos.Sprint != "sprint-2" || pos.Stage != "planning" {
		t.Errorf("carried task: %+v", pos)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("carried symlink broken: %v", err)
	}

	// A new next sprint exists; exactly one active.
	active, _ := ws.ActiveSprint()
	next, _ := ws.NextSprint()
	if active.Name != "sprint-2" || next.Name != "sprint-3" {
		t.Errorf("after close: active=%s next=%s", active.Name, next.Name)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix flaky auth test": "fix-flaky-auth-test",
		"Добавить экспорт отчётов в CSV": "dobavit-eksport-otchetov-v-csv",
		"  ---  ": "task",
		"A very long task title that does not fit into the slug length limit at all": "a-very-long-task-title-that-does-not-fit",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMdSections(t *testing.T) {
	body := "## Objective\nOld text.\n"
	body = SetSection(body, "Context", "Found in src/x.go")
	body = SetSection(body, "Objective", "New text.")
	if got := GetSection(body, "Objective"); got != "New text." {
		t.Errorf("Objective = %q", got)
	}
	if got := GetSection(body, "Context"); got != "Found in src/x.go" {
		t.Errorf("Context = %q", got)
	}
	if got := GetSection(body, "Missing"); got != "" {
		t.Errorf("Missing = %q", got)
	}
}

func mustHistory(t *testing.T, dir string) []HistoryEvent {
	t.Helper()
	events, err := ReadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	return events
}
