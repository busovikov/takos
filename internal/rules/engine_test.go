package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

func newWS(t *testing.T) (*core.Workspace, *core.Task) {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("Rules test task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return ws, task
}

// spawnSpy collects spawn requests instead of running agents.
type spawnSpy struct {
	mu    sync.Mutex
	specs []SpawnSpec
	err   error
}

func (s *spawnSpy) fn(ctx context.Context, taskID string, spec SpawnSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.specs = append(s.specs, spec)
	return nil
}

func (s *spawnSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.specs)
}

func hasRuleRecord(t *testing.T, taskDir, ruleID string) bool {
	t.Helper()
	events, err := core.ReadHistory(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Verb == "rule" && strings.HasPrefix(e.Args, ruleID+" #") {
			return true
		}
	}
	return false
}

func TestEnterRuleFiresExactlyOnce(t *testing.T) {
	ws, task := newWS(t)
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	spy := &spawnSpy{}
	e := New(ws, "w1", nil, spy.fn)

	acts := e.Scan(context.Background())
	if spy.count() != 1 {
		t.Fatalf("spawns = %d, want 1 (%v)", spy.count(), acts)
	}
	spec := spy.specs[0]
	if spec.Role != "context" {
		t.Errorf("role = %s, want context", spec.Role)
	}
	// The trigger event is the move (created=#0, move=#1) — the token pins
	// workspace, task, rule and event, so runner-daemon retries are replays.
	want := fmt.Sprintf("rule:w1:%s:context-on-planning:1", task.Meta.ID)
	if spec.ClientToken != want {
		t.Errorf("token = %s, want %s", spec.ClientToken, want)
	}
	if !hasRuleRecord(t, task.Dir, "context-on-planning") {
		t.Error("no ledger record after firing")
	}
	// Replay safety: further scans do not fire again.
	e.Scan(context.Background())
	New(ws, "w1", nil, spy.fn).Scan(context.Background()) // fresh engine = daemon restart
	if spy.count() != 1 {
		t.Errorf("spawns after rescans = %d, want 1", spy.count())
	}
}

func TestStaleEnterDoesNotFire(t *testing.T) {
	ws, task := newWS(t)
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	// The task has already moved on before the engine ever saw the enter.
	if err := core.SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}
	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 0 {
		t.Errorf("stale enter planning fired: %+v", spy.specs)
	}
}

func TestSessionFinishedIfFilter(t *testing.T) {
	ws, task := newWS(t)
	ws.WF.Rules = []core.Rule{{
		ID: "review-after-handover", On: "session-finished",
		If:    "role == handover && outcome == finished",
		Spawn: &core.SpawnAction{Role: "reviewer"},
	}}
	// The handover finish is followed by an unrelated digest finish — the
	// filter must reach past it instead of matching only the newest event.
	core.AppendHistory(task.Dir, "agent:handover", "session", "finished T-x-review#1")
	core.AppendHistory(task.Dir, "agent:summarizer", "session", "finished T-x-review-handoff#1")

	spy := &spawnSpy{}
	e := New(ws, "w1", nil, spy.fn)
	e.Scan(context.Background())
	if spy.count() != 1 || spy.specs[0].Role != "reviewer" {
		t.Fatalf("spawns = %+v, want one reviewer", spy.specs)
	}
	e.Scan(context.Background())
	if spy.count() != 1 {
		t.Errorf("re-fired on rescan")
	}

	// A limit ending must not pass the filter.
	core.AppendHistory(task.Dir, "agent:handover", "session", "limit T-x-review#2")
	e.Scan(context.Background())
	if spy.count() != 1 {
		t.Errorf("limit outcome fired the rule")
	}
}

func TestCheckTriggerMoveAndNotify(t *testing.T) {
	ws, task := newWS(t)
	ws.WF.Rules = []core.Rule{{
		ID: "go-on-check", On: "check go",
		Then: []core.Action{{Notify: "ready to plan"}, {Move: "planning"}},
	}}
	core.SetCheck(task.Dir, "go")
	core.AppendHistory(task.Dir, "user", "check", "go")

	New(ws, "w1", nil, (&spawnSpy{}).fn).Scan(context.Background())

	pos, err := ws.FindHandler(task.Meta.ID)
	if err != nil || pos.Stage != "planning" {
		t.Fatalf("stage = %v (%v), want planning", pos, err)
	}
	events, _ := core.ReadHistory(task.Dir)
	var notified bool
	for _, e := range events {
		if e.Verb == "notify" && e.Args == "ready to plan" {
			notified = true
		}
	}
	if !notified {
		t.Error("no notify record")
	}
	if !hasRuleRecord(t, task.Dir, "go-on-check") {
		t.Error("no ledger record")
	}
}

func TestBusyTaskDefersSpawn(t *testing.T) {
	ws, task := newWS(t)
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	// A live session occupies the slot (our own pid is alive).
	session.Begin(task.Dir, "user", "T-x-planning#1", "uuid", os.Getpid(), false)

	spy := &spawnSpy{}
	e := New(ws, "w1", nil, spy.fn)
	e.Scan(context.Background())
	if spy.count() != 0 {
		t.Fatalf("fired while a session is live")
	}
	if hasRuleRecord(t, task.Dir, "context-on-planning") {
		t.Fatal("claimed the trigger while deferred — the retry is lost")
	}

	// The session ends — the deferred rule fires on the next scan.
	session.Finish(task.Dir, "user", "T-x-planning#1", "uuid", session.OutcomeFinished)
	e.Scan(context.Background())
	if spy.count() != 1 {
		t.Errorf("deferred rule did not fire after the session ended")
	}
}

func TestBudgetExhaustion(t *testing.T) {
	ws, task := newWS(t)
	ws.WF.Settings.RuleBudgetPerTaskHour = 1
	ws.WF.Rules = []core.Rule{
		{ID: "first", On: "check go", Then: []core.Action{{Notify: "one"}}},
		{ID: "second", On: "check go", Then: []core.Action{{Notify: "two"}}},
	}
	core.SetCheck(task.Dir, "go")
	core.AppendHistory(task.Dir, "user", "check", "go")

	e := New(ws, "w1", nil, (&spawnSpy{}).fn)
	acts := e.Scan(context.Background())
	if !hasRuleRecord(t, task.Dir, "first") {
		t.Error("first rule did not fire")
	}
	if hasRuleRecord(t, task.Dir, "second") {
		t.Error("second rule fired past the budget")
	}
	deferred := false
	for _, a := range acts {
		if strings.Contains(a, "budget") {
			deferred = true
		}
	}
	if !deferred {
		t.Errorf("no budget action line: %v", acts)
	}
}

func TestAutomationPausedSkipsTask(t *testing.T) {
	ws, task := newWS(t)
	task.Meta.Automation = "paused"
	if err := task.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 0 {
		t.Errorf("paused task fired rules")
	}
}

// TestInProcessSpawnFallback exercises the default spawner with no runner
// daemon around: the job runs inside the engine, transcript and history
// land in the task folder as with any headless run.
func TestInProcessSpawnFallback(t *testing.T) {
	t.Setenv("TAKOS_HOME", t.TempDir()) // no runner daemon here
	mockAgent(t, "echo 'context written'\nexit 0")
	ws, task := newWS(t)
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}

	e := New(ws, "w1", nil, nil)
	e.Scan(context.Background())

	waitCond(t, 10*time.Second, func() bool {
		events, _ := core.ReadHistory(task.Dir)
		for _, ev := range events {
			if ev.Actor == "agent:context" && ev.Verb == "session" &&
				strings.HasPrefix(ev.Args, "finished") {
				return true
			}
		}
		return false
	})
	e.Wait()

	// While the job ran (and after), rescans must not double-fire.
	e.Scan(context.Background())
	events, _ := core.ReadHistory(task.Dir)
	starts := 0
	for _, ev := range events {
		if ev.Verb == "session" && strings.HasPrefix(ev.Args, "start ") {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d session starts, want 1: %v", starts, events)
	}
}

// mockAgent puts a fake `claude` on PATH (same contract as the runnerd
// tests): --version probe plus a scripted body.
func mockAgent(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then echo mock 1.0; exit 0; fi\n" +
		script
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
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
