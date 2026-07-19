package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
)

func waitPing(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case <-w.C:
	case <-time.After(3 * time.Second):
		t.Fatal("no ping from the watcher")
	}
}

func TestWatcherPingsOnHandlerAreaChanges(t *testing.T) {
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := New(ws.Root, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// A change in the backlog.
	if err := os.Symlink("../tasks/T-0001-x", filepath.Join(ws.BacklogDir(), "T-0001-x")); err != nil {
		t.Fatal(err)
	}
	waitPing(t, w)

	// A change inside a stage folder of a sprint created BEFORE the watcher.
	active, _ := ws.ActiveSprint()
	if err := os.Symlink("../../../tasks/T-0002-y",
		filepath.Join(active.Dir, "1-refinement", "T-0002-y")); err != nil {
		t.Fatal(err)
	}
	waitPing(t, w)

	// A brand-new sprint folder gets watched on the fly.
	s, err := ws.CreateSprint(core.SprintNext, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	waitPing(t, w) // the creation burst itself
	if err := os.Symlink("../../../tasks/T-0003-z",
		filepath.Join(s.Dir, "1-refinement", "T-0003-z")); err != nil {
		t.Fatal(err)
	}
	waitPing(t, w)
}
