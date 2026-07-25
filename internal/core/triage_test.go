package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inReview creates a task, walks it to the review stage and gives it a
// review round with the supplied findings (all open).
func inReview(t *testing.T, titles ...string) (*Workspace, *Task) {
	t.Helper()
	ws := newTestWS(t)
	task, _, err := ws.CreateTask("Triage subject", CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"planning", "implementation", "review"} {
		if stage == "implementation" {
			if err := SetCheck(task.Dir, "plan-resolved"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ws.MoveTask(task.Meta.ID, stage, "user", ""); err != nil {
			t.Fatalf("move to %s: %v", stage, err)
		}
	}

	var b strings.Builder
	b.WriteString("# Review\n\n")
	for i, title := range titles {
		b.WriteString("## [R" + string(rune('1'+i)) + "] " + title + " — open\nbody of finding\n\n")
	}
	path, err := NewRoundPath(task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, task
}

func TestTriageResolvedRoundLeavesTaskInReviewForAHumanToClose(t *testing.T) {
	ws, task := inReview(t, "First finding", "Second finding")
	id := task.Meta.ID

	// A partially triaged round has no verdict and stays put.
	res, err := ws.TriageFinding(id, "R1", ReviewResolved)
	if err != nil {
		t.Fatal(err)
	}
	if res.Done || res.MovedTo != "" {
		t.Fatalf("partial triage: %+v, want no verdict", res)
	}
	pos, _ := ws.FindHandler(id)
	if pos.Stage != "review" {
		t.Fatalf("stage = %s, want review", pos.Stage)
	}

	// The last decision computes the verdict — but a "done" verdict does
	// not auto-move the task; it stays in review until someone confirms it.
	res, err = ws.TriageFinding(id, "R2", ReviewResolved)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Done || res.Verdict != "done" || res.MovedTo != "" {
		t.Fatalf("final triage: %+v, want verdict done and no auto-move", res)
	}
	pos, _ = ws.FindHandler(id)
	if pos.Stage != "review" {
		t.Errorf("stage = %s, want review (done verdict must not auto-move)", pos.Stage)
	}

	// A human can now confirm the move, since review:clean is satisfied.
	if _, err := ws.MoveTask(id, "done", "user", ""); err != nil {
		t.Fatalf("manual move to done should be allowed once findings are clean: %v", err)
	}
}

func TestTriageReworkReturnsToImplementation(t *testing.T) {
	ws, task := inReview(t, "Needs another pass", "Fine as is")
	id := task.Meta.ID

	if _, err := ws.TriageFinding(id, "R1", ReviewRework); err != nil {
		t.Fatal(err)
	}
	res, err := ws.TriageFinding(id, "R2", ReviewResolved)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "rework" || res.MovedTo != "implementation" {
		t.Fatalf("rework triage: %+v", res)
	}
	pos, _ := ws.FindHandler(id)
	if pos.Stage != "implementation" {
		t.Errorf("stage = %s, want implementation", pos.Stage)
	}

	// The rework finding is waiting for the executor in hand-off.md.
	data, err := os.ReadFile(filepath.Join(task.Dir, HandoffFileName))
	if err != nil {
		t.Fatal(err)
	}
	sec := GetSection(string(data), "From review")
	if !strings.Contains(sec, "Needs another pass") {
		t.Errorf("From review misses the rework finding:\n%s", sec)
	}
	if strings.Contains(sec, "Fine as is") {
		t.Errorf("a resolved finding leaked into the hand-off:\n%s", sec)
	}
}

func TestTriageTechDebtSpawnsBacklogTask(t *testing.T) {
	ws, task := inReview(t, "Slow query nobody asked about")

	res, err := ws.TriageFinding(task.Meta.ID, "R1", ReviewTechDebt)
	if err != nil {
		t.Fatal(err)
	}
	if res.DebtID == "" {
		t.Fatal("no tech-debt task was created")
	}
	debtDir, err := ws.FindTaskDir(res.DebtID)
	if err != nil {
		t.Fatal(err)
	}
	debt, err := LoadTask(debtDir)
	if err != nil {
		t.Fatal(err)
	}
	if debt.Meta.Title != "Slow query nobody asked about" {
		t.Errorf("debt title = %q", debt.Meta.Title)
	}
	// It links back to where it came from, and it lands in the backlog.
	if obj := GetSection(debt.Body, "Objective"); !strings.Contains(obj, task.Meta.ID) {
		t.Errorf("objective does not name the source task:\n%s", obj)
	}
	pos, err := ws.FindHandler(res.DebtID)
	if err != nil || pos.Area != AreaBacklog {
		t.Errorf("debt handler = %+v (%v), want the backlog", pos, err)
	}
}

func TestTriageUnknownFinding(t *testing.T) {
	ws, task := inReview(t, "Only finding")
	if _, err := ws.TriageFinding(task.Meta.ID, "R9", ReviewResolved); err == nil {
		t.Error("triaging an unknown finding must be refused")
	}
}
