package session

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
)

// The mock layer: a fake `claude` on PATH records its argv and exits per
// scenario — the whole plan→build→run→classify chain runs for real, only
// the agent is a shell script.

func mockAgent(t *testing.T, script string) (dir, outFile string) {
	t.Helper()
	dir = t.TempDir()
	outFile = filepath.Join(dir, "argv.txt")
	// --version is answered directly: agent detection probes it and must
	// not disturb the recorded argv of the actual session run.
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then echo mock 1.0; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" > " + outFile + "\n" + script
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH")) // the mock shadows any real claude
	return dir, outFile
}

func newSessionWS(t *testing.T) (*core.Workspace, *core.Task) {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("Session test task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	return ws, task
}

func mustPlanAndBuild(t *testing.T, ws *core.Workspace, id string, resume bool) *Exec {
	t.Helper()
	task, pos, plan, err := Plan(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Build(ws, task, pos, plan, resume, "")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestMockFirstRunArgvAndOutcome(t *testing.T) {
	_, out := mockAgent(t, "exit 0")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	task2, pos, plan, err := Plan(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeNew || plan.NewGen != 1 {
		t.Fatalf("first entry plan: %+v", plan)
	}
	e, err := Build(ws, task2, pos, plan, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Role != "planner" {
		t.Errorf("role = %s, want planner", e.Role)
	}
	outcome, err := e.Run()
	if err != nil || outcome != OutcomeFinished {
		t.Fatalf("run: %s, %v", outcome, err)
	}

	// The mock saw exactly the contract argv (the prompt is multi-line, so
	// it spans the remaining lines of the record).
	lines := readLines(t, out)
	wantUUID := UUID(Name(id, "planning", 1))
	if len(lines) < 3 || lines[0] != "--session-id" || lines[1] != wantUUID {
		t.Errorf("argv = %q, want [--session-id %s <prompt>]", lines, wantUUID)
	}
	if prompt := strings.Join(lines[2:], "\n"); !strings.Contains(prompt, id) {
		t.Errorf("prompt does not mention the task: %q", prompt)
	}

	// History and marker recorded the lifecycle.
	events, _ := core.ReadHistory(task.Dir)
	if !hasSession(events, "start", 1) || !hasSession(events, "finished", 1) {
		t.Errorf("history misses start/finished: %v", events)
	}
	ms := Markers(task.Dir)
	if len(ms) != 1 || ms[0].Outcome != OutcomeFinished || ms[0].EndedAt.IsZero() {
		t.Errorf("marker: %+v", ms)
	}
}

func TestMockInterruptedThenResume(t *testing.T) {
	_, out := mockAgent(t, "exit 1")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	if outcome, _ := mustPlanAndBuild(t, ws, id, false).Run(); outcome != OutcomeInterrupted {
		t.Fatalf("nonzero exit classified as %s", outcome)
	}

	// The policy now asks; continuing resumes the same UUID.
	_, _, plan, err := Plan(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeAsk || plan.ResumeName != Name(id, "planning", 1) {
		t.Fatalf("after interrupt: %+v", plan)
	}
	if outcome, _ := mustPlanAndBuild(t, ws, id, true).Run(); outcome != OutcomeInterrupted {
		t.Fatal("resume run lost")
	}
	lines := readLines(t, out)
	if len(lines) != 2 || lines[0] != "--resume" || lines[1] != UUID(Name(id, "planning", 1)) {
		t.Errorf("resume argv = %q", lines)
	}
}

func TestMockNewGenerationAfterFinished(t *testing.T) {
	_, out := mockAgent(t, "exit 0")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	mustPlanAndBuild(t, ws, id, false).Run()
	_, _, plan, err := Plan(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	// planning's finished exception: still offers a fresh generation #2,
	// but as ModeAsk (continue/new) rather than forcing ModeNew.
	if plan.Mode != ModeAsk || plan.NewGen != 2 {
		t.Fatalf("after finished: %+v", plan)
	}
	mustPlanAndBuild(t, ws, id, false).Run()
	lines := readLines(t, out)
	if lines[1] != UUID(Name(id, "planning", 2)) {
		t.Errorf("second generation UUID = %s, want %s", lines[1], UUID(Name(id, "planning", 2)))
	}
}

func TestCancelSendsSIGTERM(t *testing.T) {
	mockAgent(t, "trap 'exit 143' TERM\nwhile true; do sleep 0.1; done")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	e := mustPlanAndBuild(t, ws, id, false)
	done := make(chan string, 1)
	go func() {
		outcome, _ := e.Run()
		done <- outcome
	}()

	// The session appears as running, then x/cancel interrupts it.
	waitCond(t, 3*time.Second, func() bool { return Active(task.Dir) != nil })
	if _, err := Cancel(task.Dir); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome != OutcomeInterrupted {
			t.Errorf("outcome = %s, want interrupted", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not die after SIGTERM")
	}
	if Active(task.Dir) != nil {
		t.Error("session still counts as running")
	}
	events, _ := core.ReadHistory(task.Dir)
	if !hasSession(events, "interrupted", 1) {
		t.Error("no interrupted record in history")
	}
}

func TestReleaseMarksFinishedWithoutStopping(t *testing.T) {
	mockAgent(t, "trap 'exit 143' TERM\nwhile true; do sleep 0.1; done")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	e := mustPlanAndBuild(t, ws, id, false)
	done := make(chan string, 1)
	go func() {
		outcome, _ := e.Run()
		done <- outcome
	}()

	waitCond(t, 3*time.Second, func() bool { return Active(task.Dir) != nil })
	m, err := Release(task.Dir)
	if err != nil {
		t.Fatal(err)
	}

	// The marker reads finished right away — the process itself is
	// untouched, still running.
	if Active(task.Dir) != nil {
		t.Error("session still counts as running after Release")
	}
	events, _ := core.ReadHistory(task.Dir)
	if !hasSession(events, "finished", 1) {
		t.Error("no finished record in history")
	}
	select {
	case <-done:
		t.Fatal("process exited on its own — Release should not have touched it")
	case <-time.After(200 * time.Millisecond):
	}

	// Clean up: the mock agent only dies on SIGTERM.
	syscall.Kill(m.PID, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mock agent did not die after cleanup SIGTERM")
	}
}

func TestSecondSessionRefusedWhileRunning(t *testing.T) {
	mockAgent(t, "trap 'exit 143' TERM\nwhile true; do sleep 0.1; done")
	ws, task := newSessionWS(t)
	e := mustPlanAndBuild(t, ws, task.Meta.ID, false)
	go e.Run()
	waitCond(t, 3*time.Second, func() bool { return Active(task.Dir) != nil })
	defer Cancel(task.Dir)

	if _, _, _, err := Plan(ws, task.Meta.ID); err == nil ||
		!strings.Contains(err.Error(), "already running") {
		t.Errorf("parallel session not refused: %v", err)
	}
}

func TestStaleMarkerRepairedAfterKill9(t *testing.T) {
	mockAgent(t, "exit 0")
	ws, task := newSessionWS(t)
	id := task.Meta.ID

	// Simulate takos killed -9 mid-session: a start record + a marker with
	// a dead pid, no terminal record.
	name := Name(id, "planning", 1)
	writeMarker(task.Dir, &Marker{
		Name: name, UUID: UUID(name), PID: 1 << 30, StartedAt: time.Now(),
	})
	core.AppendHistory(task.Dir, "user", "session", "start "+name+" "+UUID(name))

	_, _, plan, err := Plan(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeAsk || plan.ResumeName != name {
		t.Errorf("after crash repair: %+v", plan)
	}
	ms := Markers(task.Dir)
	if len(ms) != 1 || ms[0].Outcome != OutcomeInterrupted {
		t.Errorf("stale marker not finalized: %+v", ms)
	}
}

func hasSession(events []core.HistoryEvent, verb string, gen int) bool {
	for _, e := range events {
		if e.Verb == "session" && strings.HasPrefix(e.Args, verb+" ") &&
			strings.Contains(e.Args, "#"+string(rune('0'+gen))) {
			return true
		}
	}
	return false
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func waitCond(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
