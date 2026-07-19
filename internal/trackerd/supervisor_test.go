package trackerd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
)

// TestSupervisorMultiWorkspace is the A7 integration test: one daemon over
// two registered workspaces — invalid manual moves roll back in both — and
// a third workspace added to the registry is picked up without a restart.
func TestSupervisorMultiWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TAKOS_HOME", home)
	t.Setenv("TAKOS_WORKSPACE", "")

	newWS := func(name string) (*core.Workspace, *core.Task) {
		t.Helper()
		ws, err := core.Init(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		task, _, err := ws.CreateTask("Task in "+name, core.CreateOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return ws, task
	}
	ws1, task1 := newWS("alpha")
	ws2, task2 := newWS("beta")

	reg := &registry.Registry{}
	for name, ws := range map[string]*core.Workspace{"alpha": ws1, "beta": ws2} {
		if _, err := reg.Add(name, ws.Root); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- Run(ctx, Options{Addr: "127.0.0.1:0"}) }()
	defer func() {
		cancel()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not shut down")
		}
	}()

	addr := waitAddr(t, home)
	waitState(t, addr, map[string]string{"alpha": StateRunning, "beta": StateRunning})

	// An invalid manual mv in EACH workspace is rolled back independently.
	for _, tc := range []struct {
		ws   *core.Workspace
		task *core.Task
	}{{ws1, task1}, {ws2, task2}} {
		pos, err := tc.ws.FindHandler(tc.task.Meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		active, _ := tc.ws.ActiveSprint()
		dest := filepath.Join(active.Dir, "5-done", filepath.Base(pos.Path))
		if err := os.Rename(pos.Path, dest); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		ws   *core.Workspace
		task *core.Task
	}{{ws1, task1}, {ws2, task2}} {
		waitFor(t, 5*time.Second, func() error {
			pos, err := tc.ws.FindHandler(tc.task.Meta.ID)
			if err != nil {
				return err
			}
			if pos.Stage != "refinement" {
				return fmt.Errorf("%s still in %s", tc.task.Meta.ID, pos.Stage)
			}
			return nil
		})
	}

	// Hot-reload: a workspace registered while the daemon runs is picked up.
	ws3, _ := newWS("gamma")
	reg, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Add("gamma", ws3.Root); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	waitState(t, addr, map[string]string{
		"alpha": StateRunning, "beta": StateRunning, "gamma": StateRunning,
	})
}

// waitAddr polls the pid file for the daemon's actual API address.
func waitAddr(t *testing.T, home string) string {
	t.Helper()
	var addr string
	waitFor(t, 5*time.Second, func() error {
		data, err := os.ReadFile(filepath.Join(home, GlobalPidFile))
		if err != nil {
			return err
		}
		lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
		if len(lines) < 2 {
			return fmt.Errorf("pid file incomplete")
		}
		addr = strings.TrimSpace(lines[1])
		return nil
	})
	return addr
}

// waitState polls /api/workspaces until every named workspace reports the
// wanted state.
func waitState(t *testing.T, addr string, want map[string]string) {
	t.Helper()
	waitFor(t, 5*time.Second, func() error {
		resp, err := http.Get("http://" + addr + "/api/workspaces")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var wss []WorkspaceSummary
		if err := json.NewDecoder(resp.Body).Decode(&wss); err != nil {
			return err
		}
		got := map[string]string{}
		for _, ws := range wss {
			got[ws.Name] = ws.State
		}
		for name, state := range want {
			if got[name] != state {
				return fmt.Errorf("%s: state %q, want %q (all: %v)", name, got[name], state, got)
			}
		}
		return nil
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = cond(); last == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %v", timeout, last)
}
