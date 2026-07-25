package trackerd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
)

// TestAutomationChainThroughDaemon is the B4 integration test: the tracker
// daemon alone (no runner daemon — jobs run in-process) drives the default
// rule chain on a mock agent. Entering planning spawns the context role;
// entering review spawns handover, whose finish spawns the reviewer.
func TestAutomationChainThroughDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TAKOS_HOME", home)
	t.Setenv("TAKOS_WORKSPACE", "")

	// The mock claude finishes instantly for every role.
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo mock 1.0; exit 0; fi\necho role work done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	ws, err := core.Init(filepath.Join(home, "auto"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("Automation chain", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Entered planning while no daemon was running — the startup scan must
	// pick the pending trigger up.
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}

	reg := &registry.Registry{}
	if _, err := reg.Add("auto", ws.Root); err != nil {
		t.Fatal(err)
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
		case <-time.After(15 * time.Second):
			t.Error("daemon did not shut down")
		}
	}()

	sessionDone := func(role string) func() error {
		return func() error {
			events, err := core.ReadHistory(task.Dir)
			if err != nil {
				return err
			}
			for _, e := range events {
				if e.Actor == "agent:"+role && e.Verb == "session" &&
					strings.HasPrefix(e.Args, "finished") {
					return nil
				}
			}
			return fmt.Errorf("no finished %s session yet", role)
		}
	}

	// context-on-planning fires on the daemon's startup scan.
	waitFor(t, 15*time.Second, sessionDone("context"))

	// The user drives the task into review; the daemon notices the entered
	// stage, runs handover, and chains the reviewer off its finish.
	if err := core.SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(task.Meta.ID, "review", "user", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, sessionDone("handover"))
	waitFor(t, 20*time.Second, sessionDone("reviewer"))

	// Every firing is on the ledger exactly once — the chain does not loop.
	events, _ := core.ReadHistory(task.Dir)
	fired := map[string]int{}
	for _, e := range events {
		if e.Verb == "rule" {
			fired[strings.Fields(e.Args)[0]]++
		}
	}
	for _, rule := range []string{"context-on-planning", "handover-on-review", "review-after-handover"} {
		if fired[rule] != 1 {
			t.Errorf("rule %s fired %d times, want 1\nhistory: %v", rule, fired[rule], events)
		}
	}
}
