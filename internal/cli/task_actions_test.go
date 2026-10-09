package cli

import (
	"path/filepath"
	"testing"

	"github.com/busovikov/takos/internal/core"
)

func TestRunAutomateRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	ws, err := core.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := ws.CreateTask("Automate test task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}

	oldFlag := workspaceFlag
	workspaceFlag = root
	defer func() { workspaceFlag = oldFlag }()

	if err := runAutomate(task.Meta.ID, false); err != nil {
		t.Fatal(err)
	}
	reloaded, err := core.LoadTask(task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Meta.Automation != "paused" {
		t.Errorf("automation = %q, want paused", reloaded.Meta.Automation)
	}

	if err := runAutomate(task.Meta.ID, true); err != nil {
		t.Fatal(err)
	}
	reloaded, err = core.LoadTask(task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Meta.Automation != "active" {
		t.Errorf("automation = %q, want active", reloaded.Meta.Automation)
	}

	events, err := core.ReadHistory(task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var automationEvents int
	for _, e := range events {
		if e.Verb == "automation" {
			automationEvents++
		}
	}
	if automationEvents != 2 {
		t.Errorf("automation history events = %d, want 2", automationEvents)
	}
}
