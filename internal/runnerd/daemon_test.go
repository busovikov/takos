package runnerd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/session"
)

// startTestDaemon runs the daemon in-process on a random port under a
// throwaway TAKOS_HOME and returns a connected client plus a stop func
// (idempotent; also wired into Cleanup).
func startTestDaemon(t *testing.T) (*Client, func()) {
	t.Helper()
	t.Setenv("TAKOS_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Addr: "127.0.0.1:0"}) }()

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("daemon: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Error("daemon did not shut down")
			}
		})
	}
	t.Cleanup(stop)

	// Connect needs the lock held and the pid file (with the real port).
	var c *Client
	waitCond(t, 5*time.Second, func() bool {
		cl, err := Connect()
		if err != nil {
			return false
		}
		if _, err := cl.Status(); err != nil {
			return false
		}
		c = cl
		return true
	})
	return c, stop
}

// registerWS puts the workspace into the registry under name — the daemon
// addresses workspaces by their registry names.
func registerWS(t *testing.T, ws *core.Workspace, name string) {
	t.Helper()
	reg := &registry.Registry{}
	if _, err := reg.Add(name, ws.Root); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

func waitJob(t *testing.T, c *Client, id string) JobInfo {
	t.Helper()
	var info JobInfo
	waitCond(t, 15*time.Second, func() bool {
		j, err := c.Job(id)
		if err != nil {
			return false
		}
		info = j
		return j.State != JobRunning
	})
	return info
}

func TestDaemonRunsJobToDone(t *testing.T) {
	mockAgent(t, "echo 'daemon work'\nexit 0")
	c, _ := startTestDaemon(t)
	ws, task := newRunnerWS(t)
	registerWS(t, ws, "w1")

	info, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: NewToken()})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != JobRunning || info.Role != "planner" || info.Workspace != "w1" {
		t.Fatalf("submit: %+v", info)
	}

	final := waitJob(t, c, info.ID)
	if final.State != JobDone || final.Outcome != session.OutcomeFinished {
		t.Fatalf("final: %+v", final)
	}
	// The transcript and history landed in the task folder as usual.
	data, err := os.ReadFile(final.LogPath)
	if err != nil || !strings.Contains(string(data), "daemon work") {
		t.Errorf("transcript: %q, %v", data, err)
	}
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
	st, err := c.Status()
	if err != nil || st.Jobs[JobDone] != 1 {
		t.Errorf("status: %+v, %v", st, err)
	}
}

func TestClientTokenIdempotency(t *testing.T) {
	mockAgent(t, "sleep 0.5\nexit 0")
	c, _ := startTestDaemon(t)
	ws, task := newRunnerWS(t)
	registerWS(t, ws, "w1")

	token := NewToken()
	first, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: token})
	if err != nil {
		t.Fatal(err)
	}
	// A retry while the job runs replays the record — no second start.
	replay, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: token})
	if err != nil || replay.ID != first.ID {
		t.Fatalf("running replay: %+v, %v (want id %s)", replay, err, first.ID)
	}
	waitJob(t, c, first.ID)
	// A retry after the job is done still replays instead of re-running.
	replay, err = c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: token})
	if err != nil || replay.ID != first.ID || replay.State != JobDone {
		t.Fatalf("done replay: %+v, %v", replay, err)
	}
	events, _ := core.ReadHistory(task.Dir)
	starts := 0
	for _, e := range events {
		if e.Verb == "session" && strings.HasPrefix(e.Args, "start ") {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d session starts after replays, want exactly 1: %v", starts, events)
	}

	// A fresh token is a real submit: the next generation runs (preceded by
	// its hand-off digest — the summarizer shares the mock agent). Planning's
	// finished session collapses to resume headlessly unless New is set.
	second, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: NewToken(), New: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Session == first.Session {
		t.Fatalf("fresh token did not start a new generation: %+v", second)
	}
	if final := waitJob(t, c, second.ID); final.State != JobDone {
		t.Fatalf("second job: %+v", final)
	}
}

func TestDaemonSerializesPerTaskAndCancels(t *testing.T) {
	mockAgent(t, "echo running\nwhile true; do sleep 0.1; done")
	c, _ := startTestDaemon(t)
	ws, task := newRunnerWS(t)
	registerWS(t, ws, "w1")

	first, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: NewToken()})
	if err != nil {
		t.Fatal(err)
	}
	// A parallel submit with a different token is refused while the first
	// job runs — even before the child's marker hits the disk.
	if _, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: NewToken()}); err == nil {
		t.Fatal("second concurrent submit must be refused")
	}

	if _, err := c.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	final := waitJob(t, c, first.ID)
	if final.State != JobDone || final.Outcome != session.OutcomeInterrupted {
		t.Fatalf("after cancel: %+v", final)
	}
	// The task is free again: cancelling one job must not wedge the slot.
	if _, err := c.Cancel(first.ID); err == nil {
		t.Error("cancelling a finished job must be refused")
	}
}

func TestDaemonShutdownInterruptsJobs(t *testing.T) {
	mockAgent(t, "echo running\nwhile true; do sleep 0.1; done")
	c, stop := startTestDaemon(t)
	ws, task := newRunnerWS(t)
	registerWS(t, ws, "w1")

	info, err := c.Submit("w1", task.Meta.ID, SubmitRequest{})
	if err != nil {
		t.Fatal(err)
	}
	waitCond(t, 5*time.Second, func() bool { return session.Active(task.Dir) != nil })

	// Shutdown waits for the SIGTERM'ed job to record its ending — nothing
	// is left as a ghost "running" marker.
	stop()
	ms := session.Markers(task.Dir)
	if len(ms) == 0 || ms[len(ms)-1].Outcome != session.OutcomeInterrupted {
		t.Fatalf("markers after shutdown: %+v", ms)
	}
	_ = info
}

func TestConnectWithoutDaemon(t *testing.T) {
	t.Setenv("TAKOS_HOME", t.TempDir())
	if _, err := Connect(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Connect = %v, want ErrNotRunning", err)
	}
}

func TestSubmitUnknownWorkspaceAndTask(t *testing.T) {
	mockAgent(t, "exit 0")
	c, _ := startTestDaemon(t)
	ws, _ := newRunnerWS(t)
	registerWS(t, ws, "w1")

	if _, err := c.Submit("nope", "T-0001", SubmitRequest{}); err == nil ||
		!strings.Contains(err.Error(), "unknown workspace") {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := c.Submit("w1", "T-9999", SubmitRequest{}); err == nil {
		t.Error("unknown task must be refused")
	}
}

// TestDaemonResumesSessionByName drives the failure path over the API: the
// submit's Session field re-enters the limited session instead of planning
// a stage entry.
func TestDaemonResumesSessionByName(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "resumed")
	mockAgent(t, "if [ -f "+flag+" ]; then echo 'done now'; exit 0; fi\n"+
		"touch "+flag+"\necho 'You have hit your usage limit.'\nexit 1")
	c, _ := startTestDaemon(t)
	ws, task := newRunnerWS(t)
	registerWS(t, ws, "w1")

	first, err := c.Submit("w1", task.Meta.ID, SubmitRequest{ClientToken: NewToken()})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, c, first.ID); got.Outcome != session.OutcomeLimit {
		t.Fatalf("first job: %+v, want limit", got)
	}

	res, err := c.Submit("w1", task.Meta.ID, SubmitRequest{
		Session: first.Session, Role: "planner", ClientToken: NewToken(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resume || res.Session != first.Session {
		t.Fatalf("resume job: %+v, want a resume of %s", res, first.Session)
	}
	if got := waitJob(t, c, res.ID); got.Outcome != session.OutcomeFinished {
		t.Fatalf("resume: %+v, want finished", got)
	}
}
