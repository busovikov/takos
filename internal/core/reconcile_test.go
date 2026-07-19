package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustReconcile(t *testing.T, ws *Workspace) *ReconcileReport {
	t.Helper()
	rep, err := ws.Reconcile(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestReconcileIdempotentOnCleanWorkspace(t *testing.T) {
	ws := newTestWS(t)
	ws.CreateTask("Clean one", CreateOpts{})
	rep := mustReconcile(t, ws)
	if len(rep.Actions) != 0 {
		t.Errorf("clean workspace produced repairs: %v", rep.Actions)
	}
}

func TestReconcileRestoresMissingHandler(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("Vanishing handler", CreateOpts{})
	ws.MoveTask(task.Meta.ID, "planning", "user", "")
	pos, _ := ws.FindHandler(task.Meta.ID)
	os.Remove(pos.Path) // manual rm of the handler

	rep := mustReconcile(t, ws)
	if len(rep.Actions) == 0 {
		t.Fatal("no repair reported")
	}
	pos, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pos.Stage != "planning" || pos.Area != AreaSprint {
		t.Errorf("restored to %+v, want planning in a sprint", pos)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("restored symlink broken: %v", err)
	}
}

func TestReconcileRestoresBacklogHandler(t *testing.T) {
	ws := newTestWS(t)
	task, pos, _ := ws.CreateTask("Backlog vanisher", CreateOpts{Backlog: true})
	os.Remove(pos.Path)

	mustReconcile(t, ws)
	got, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Area != AreaBacklog {
		t.Errorf("restored to %+v, want backlog", got)
	}
}

func TestReconcileDedupesHandlers(t *testing.T) {
	ws := newTestWS(t)
	task, pos, _ := ws.CreateTask("Doubled one", CreateOpts{})
	// A stray copy of the handler in another stage (cp -a of the symlink).
	name := filepath.Base(pos.Path)
	active, _ := ws.ActiveSprint()
	dup := filepath.Join(active.Dir, "3-implementation", name)
	if err := os.Symlink("../../../tasks/"+name, dup); err != nil {
		t.Fatal(err)
	}

	mustReconcile(t, ws)
	got, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != "refinement" {
		t.Errorf("survivor in %s, want refinement (per history)", got.Stage)
	}
	if _, err := os.Lstat(dup); !os.IsNotExist(err) {
		t.Error("the duplicate is still there")
	}
}

func TestReconcileQuarantinesNonSymlink(t *testing.T) {
	ws := newTestWS(t)
	active, _ := ws.ActiveSprint()
	// cp -r instead of mv: a real directory inside a stage folder.
	stray := filepath.Join(active.Dir, "1-refinement", "T-0099-copied")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}

	rep := mustReconcile(t, ws)
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("the stray directory is still in the stage folder")
	}
	if _, err := os.Stat(filepath.Join(ws.Root, QuarantineDirName, "T-0099-copied")); err != nil {
		t.Errorf("not in quarantine: %v", err)
	}
	found := false
	for _, a := range rep.Actions {
		if strings.Contains(a, "quarantined") {
			found = true
		}
	}
	if !found {
		t.Errorf("no quarantine report: %v", rep.Actions)
	}
}

func TestReconcileRepairsSymlinkTarget(t *testing.T) {
	ws := newTestWS(t)
	task, pos, _ := ws.CreateTask("Broken target", CreateOpts{})
	// Simulate a manual mv across depth levels: same name, stale target.
	os.Remove(pos.Path)
	bad := filepath.Join(ws.BacklogDir(), filepath.Base(pos.Path))
	if err := os.Symlink("../../wrong/depth/"+filepath.Base(pos.Path), bad); err != nil {
		t.Fatal(err)
	}

	mustReconcile(t, ws)
	got, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Area != AreaBacklog {
		t.Errorf("handler at %+v, want backlog (position is authority)", got)
	}
	if _, err := os.Stat(filepath.Join(got.Path, TaskFile)); err != nil {
		t.Errorf("target still broken: %v", err)
	}
	// History follows the observed position.
	stage, _ := LastMove(mustHistory(t, task.Dir))
	if stage != Backlog {
		t.Errorf("history stage = %s, want backlog", stage)
	}
}

func TestReconcileRemovesOrphanHandler(t *testing.T) {
	ws := newTestWS(t)
	orphan := filepath.Join(ws.BacklogDir(), "T-0042-ghost")
	if err := os.Symlink("../tasks/T-0042-ghost", orphan); err != nil {
		t.Fatal(err)
	}
	mustReconcile(t, ws)
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Error("orphan handler survived")
	}
}

func TestReconcileClosesOverdueSprints(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("Rides across sprints", CreateOpts{})

	// Rewind the active sprint: it ended two cadences ago.
	active, _ := ws.ActiveSprint()
	active.Meta.StartedAt = time.Now().Add(-42 * 24 * time.Hour)
	active.Meta.EndsAt = time.Now().Add(-28 * 24 * time.Hour)
	if err := SaveSprint(active.Dir, &active.Meta); err != nil {
		t.Fatal(err)
	}

	rep := mustReconcile(t, ws)
	if len(rep.Actions) < 2 {
		t.Fatalf("expected at least two overdue closes, got %v", rep.Actions)
	}
	newActive, err := ws.ActiveSprint()
	if err != nil {
		t.Fatal(err)
	}
	if newActive.Name == "sprint-1" {
		t.Error("overdue sprint-1 is still active")
	}
	if newActive.Meta.EndsAt.Before(time.Now()) {
		t.Errorf("the promoted sprint is still overdue: ends %v", newActive.Meta.EndsAt)
	}
	// The unfinished task was carried along, symlink alive.
	pos, err := ws.FindHandler(task.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pos.Sprint != newActive.Name || pos.Stage != "refinement" {
		t.Errorf("carried task: %+v, want refinement in %s", pos, newActive.Name)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("carried symlink broken: %v", err)
	}

	// Idempotency: a second pass has nothing to do.
	rep = mustReconcile(t, ws)
	if len(rep.Actions) != 0 {
		t.Errorf("second reconcile still repairs: %v", rep.Actions)
	}
}

func TestReconcileRepairsSprintPair(t *testing.T) {
	ws := newTestWS(t)
	// Kill the active sprint's metadata entirely (e.g. deleted sprint.md).
	active, _ := ws.ActiveSprint()
	os.Remove(filepath.Join(active.Dir, SprintFile))

	mustReconcile(t, ws)
	if _, err := ws.ActiveSprint(); err != nil {
		t.Errorf("no active sprint after reconcile: %v", err)
	}
	if _, err := ws.NextSprint(); err != nil {
		t.Errorf("no next sprint after reconcile: %v", err)
	}
}

func TestSyncHistoryAfterOfflineMove(t *testing.T) {
	ws := newTestWS(t)
	task, pos, _ := ws.CreateTask("Moved while daemon slept", CreateOpts{})
	// A manual mv within one sprint keeps the relative target valid.
	active, _ := ws.ActiveSprint()
	dest := filepath.Join(active.Dir, "2-planning", filepath.Base(pos.Path))
	if err := os.Rename(pos.Path, dest); err != nil {
		t.Fatal(err)
	}

	mustReconcile(t, ws)
	stage, _ := LastMove(mustHistory(t, task.Dir))
	if stage != "planning" {
		t.Errorf("history stage = %s, want planning (position is authority)", stage)
	}
}
