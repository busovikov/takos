package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

// appendOld writes a history line with a timestamp in the past — the
// failure gates measure the pause from the event, and tests cannot wait.
func appendOld(t *testing.T, taskDir string, age time.Duration, actor, verb, args string) {
	t.Helper()
	line := fmt.Sprintf("%s [%s] %s %s\n",
		time.Now().Add(-age).UTC().Format(time.RFC3339), actor, verb, args)
	f, err := os.OpenFile(filepath.Join(taskDir, core.HistoryFile),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func planningWS(t *testing.T) (*core.Workspace, *core.Task) {
	t.Helper()
	ws, task := newWS(t)
	ws.WF.Rules = nil // failure paths only — no workflow-rule noise
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	return ws, task
}

func TestLimitAutoResumeAfterPause(t *testing.T) {
	ws, task := planningWS(t)
	name := task.Meta.ID + "-planning#1"

	// Fresh limit — still inside the pause: nothing fires.
	appendOld(t, task.Dir, time.Minute, "agent:planner", "session", "start "+name+" u")
	appendOld(t, task.Dir, time.Minute, "agent:planner", "session", "limit "+name)
	spy := &spawnSpy{}
	e := New(ws, "w1", nil, spy.fn)
	e.Scan(context.Background())
	if spy.count() != 0 {
		t.Fatalf("resumed inside the pause window")
	}

	// Past the pause (default 15m): the exact session is resumed.
	appendOld(t, task.Dir, 20*time.Minute, "agent:planner", "session", "start "+name+" u")
	appendOld(t, task.Dir, 16*time.Minute, "agent:planner", "session", "limit "+name)
	e.Scan(context.Background())
	if spy.count() != 1 {
		t.Fatalf("spawns = %d, want 1", spy.count())
	}
	spec := spy.specs[0]
	if spec.Session != name || spec.Role != "planner" {
		t.Errorf("spec = %+v, want resume of %s by planner", spec, name)
	}
	if !strings.Contains(spec.ClientToken, autoResumeRule) {
		t.Errorf("token = %s, want a %s token", spec.ClientToken, autoResumeRule)
	}
	// The firing is on the ledger — no second resume for the same ending.
	e.Scan(context.Background())
	if spy.count() != 1 {
		t.Errorf("re-fired for the same limit event")
	}
}

func TestInterruptedRetriesThenPause(t *testing.T) {
	ws, task := planningWS(t)
	name := task.Meta.ID + "-planning#1"
	spy := &spawnSpy{}
	e := New(ws, "w1", nil, spy.fn)

	// First crash: one resume is spent.
	appendOld(t, task.Dir, 2*time.Minute, "agent:executor", "session", "start "+name+" u")
	appendOld(t, task.Dir, time.Minute, "agent:executor", "session", "interrupted "+name)
	e.Scan(context.Background())
	if spy.count() != 1 || spy.specs[0].Session != name {
		t.Fatalf("first crash: spawns = %+v", spy.specs)
	}

	// Second crash: the last retry.
	appendOld(t, task.Dir, time.Minute, "agent:executor", "session", "resume "+name)
	appendOld(t, task.Dir, time.Minute, "agent:executor", "session", "interrupted "+name)
	e.Scan(context.Background())
	if spy.count() != 2 {
		t.Fatalf("second crash: spawns = %d, want 2", spy.count())
	}

	// Third crash: retries exhausted — the task's automation pauses.
	appendOld(t, task.Dir, time.Minute, "agent:executor", "session", "resume "+name)
	appendOld(t, task.Dir, time.Minute, "agent:executor", "session", "interrupted "+name)
	e.Scan(context.Background())
	if spy.count() != 2 {
		t.Fatalf("resumed past sessionMaxRetries")
	}
	reloaded, err := core.LoadTask(task.Dir)
	if err != nil || reloaded.Meta.Automation != "paused" {
		t.Fatalf("automation = %q (%v), want paused", reloaded.Meta.Automation, err)
	}
	events, _ := core.ReadHistory(task.Dir)
	noted := false
	for _, ev := range events {
		if ev.Verb == "notify" && strings.Contains(ev.Args, "automation paused") {
			noted = true
		}
	}
	if !noted {
		t.Error("no pause notification in history")
	}
	// Paused means paused: nothing fires anymore.
	e.Scan(context.Background())
	if spy.count() != 2 {
		t.Errorf("a paused task kept firing")
	}
}

func TestUserSessionsAreLeftAlone(t *testing.T) {
	ws, task := planningWS(t)
	name := task.Meta.ID + "-planning#1"
	// Both the deliberate interrupt and the limit of a user session belong
	// to the user; the repair-pass terminal record must not change that.
	appendOld(t, task.Dir, 20*time.Minute, "user", "session", "start "+name+" u")
	appendOld(t, task.Dir, 16*time.Minute, "user", "session", "limit "+name)
	appendOld(t, task.Dir, 10*time.Minute, "user", "session", "resume "+name)
	appendOld(t, task.Dir, 5*time.Minute, "engine", "session", "interrupted "+name)

	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 0 {
		t.Errorf("touched a user session: %+v", spy.specs)
	}
}

func TestStaleSessionNotResumed(t *testing.T) {
	ws, task := planningWS(t)
	name := task.Meta.ID + "-planning#1"
	appendOld(t, task.Dir, 20*time.Minute, "agent:planner", "session", "start "+name+" u")
	appendOld(t, task.Dir, 16*time.Minute, "agent:planner", "session", "limit "+name)
	// The task has moved on — the planning session stays parked.
	if err := core.SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}
	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 0 {
		t.Errorf("resumed a session of a stage the task left: %+v", spy.specs)
	}
}

func TestDigestSessionResumes(t *testing.T) {
	ws, task := planningWS(t)
	// A limited hand-off digest blocks its stage — it belongs to the base
	// stage and is resumed like any agent session.
	name := task.Meta.ID + "-planning-handoff#1"
	appendOld(t, task.Dir, 20*time.Minute, "agent:summarizer", "session", "start "+name+" u")
	appendOld(t, task.Dir, 16*time.Minute, "agent:summarizer", "session", "limit "+name)

	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 1 || spy.specs[0].Session != name || spy.specs[0].Role != "summarizer" {
		t.Errorf("spawns = %+v, want the digest resumed by summarizer", spy.specs)
	}
}

func TestFinishedSessionNeedsNoHealing(t *testing.T) {
	ws, task := planningWS(t)
	name := task.Meta.ID + "-planning#1"
	appendOld(t, task.Dir, 20*time.Minute, "agent:planner", "session", "start "+name+" u")
	appendOld(t, task.Dir, 16*time.Minute, "agent:planner", "session", "limit "+name)
	// A later resume finished the session — the old limit is history.
	appendOld(t, task.Dir, 10*time.Minute, "agent:planner", "session", "resume "+name)
	appendOld(t, task.Dir, 5*time.Minute, "agent:planner", "session", "finished "+name)

	spy := &spawnSpy{}
	New(ws, "w1", nil, spy.fn).Scan(context.Background())
	if spy.count() != 0 {
		t.Errorf("healed a healthy task: %+v", spy.specs)
	}
}

// TestSessionStageOf pins the name parsing the failure paths rely on.
func TestSessionStageOf(t *testing.T) {
	cases := []struct{ name, want string }{
		{"T-0005-planning#2", "planning"},
		{"T-0005-planning-handoff#1", "planning-handoff"},
		{"T-0005-review#10", "review"},
		{"T-0006-planning#1", ""}, // another task
		{"garbage", ""},
	}
	for _, tc := range cases {
		if got := session.StageOf("T-0005", tc.name); got != tc.want {
			t.Errorf("StageOf(T-0005, %s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
