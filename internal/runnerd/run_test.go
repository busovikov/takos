package runnerd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/session"
)

// The mock agent binary: emits a transcript line by line (flushed — the
// streaming guarantee is under test), then ends per scenario.
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

func newRunnerWS(t *testing.T) (*core.Workspace, *core.Task) {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("Runner test task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	return ws, task
}

func TestJobFinishedTranscriptAndHistory(t *testing.T) {
	mockAgent(t, "echo 'step one'\necho 'step two'\nexit 0")
	ws, task := newRunnerWS(t)

	job, err := PlanJob(ws, task.Meta.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Role != "planner" || job.Actor != "agent:planner" || job.Resume {
		t.Fatalf("job: %+v", job)
	}
	outcome, err := job.Execute(context.Background())
	if err != nil || outcome != session.OutcomeFinished {
		t.Fatalf("execute: %s, %v", outcome, err)
	}

	// The transcript landed in sessions/<name>.log.
	data, err := os.ReadFile(job.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "step one") || !strings.Contains(string(data), "step two") {
		t.Errorf("transcript incomplete: %q", data)
	}
	// History carries the agent actor.
	events, _ := core.ReadHistory(task.Dir)
	found := false
	for _, e := range events {
		if e.Actor == "agent:planner" && e.Verb == "session" && strings.HasPrefix(e.Args, "finished") {
			found = true
		}
	}
	if !found {
		t.Errorf("no agent:planner finished record: %v", events)
	}
}

func TestJobLimitThenAutoResume(t *testing.T) {
	mockAgent(t, "echo 'You have hit your usage limit.'\nexit 1")
	ws, task := newRunnerWS(t)

	job, _ := PlanJob(ws, task.Meta.ID, "", false)
	outcome, err := job.Execute(context.Background())
	if err != nil || outcome != session.OutcomeLimit {
		t.Fatalf("limit run: %s, %v", outcome, err)
	}

	// The policy resumes the same session — and refuses forceNew.
	next, err := PlanJob(ws, task.Meta.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Resume || next.Name != job.Name {
		t.Errorf("after limit: %+v, want resume of %s", next, job.Name)
	}
	if strings.Contains(strings.Join(next.Argv, " "), "--session-id") {
		t.Errorf("resume argv must use --resume: %q", next.Argv)
	}
	if _, err := PlanJob(ws, task.Meta.ID, "", true); err == nil {
		t.Error("forceNew after limit must be refused")
	}
}

func TestJobStreamsWhileRunning(t *testing.T) {
	mockAgent(t, "echo 'early line'\nwhile true; do sleep 0.1; done")
	ws, task := newRunnerWS(t)

	job, _ := PlanJob(ws, task.Meta.ID, "", false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		outcome, _ := job.Execute(ctx)
		done <- outcome
	}()

	// The line is on disk while the agent still runs — real-time streaming.
	waitCond(t, 3*time.Second, func() bool {
		data, _ := os.ReadFile(job.LogPath)
		return strings.Contains(string(data), "early line")
	})
	if session.Active(task.Dir) == nil {
		t.Error("no live marker during the run")
	}

	// ctx cancel = SIGTERM: the run ends as interrupted.
	cancel()
	select {
	case outcome := <-done:
		if outcome != session.OutcomeInterrupted {
			t.Errorf("outcome = %s, want interrupted", outcome)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("job did not stop on ctx cancel")
	}
}

func TestJobCrashSafety(t *testing.T) {
	mockAgent(t, "echo 'partial work saved'\nwhile true; do sleep 0.1; done")
	ws, task := newRunnerWS(t)

	job, _ := PlanJob(ws, task.Meta.ID, "", false)
	done := make(chan string, 1)
	go func() {
		outcome, _ := job.Execute(context.Background())
		done <- outcome
	}()
	waitCond(t, 3*time.Second, func() bool { return session.Active(task.Dir) != nil })

	// The agent dies hard (kill -9) — the executor classifies interrupted
	// and the partial transcript survives.
	m := session.Active(task.Dir)
	if err := syscall.Kill(m.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome != session.OutcomeInterrupted {
			t.Errorf("outcome = %s, want interrupted", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not notice the crash")
	}
	data, _ := os.ReadFile(job.LogPath)
	if !strings.Contains(string(data), "partial work saved") {
		t.Errorf("partial transcript lost: %q", data)
	}
	// The next entry offers to continue the interrupted session.
	next, err := PlanJob(ws, task.Meta.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Resume {
		t.Errorf("after crash: %+v, want resume", next)
	}
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
