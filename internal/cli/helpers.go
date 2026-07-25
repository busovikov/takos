package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/trackerd"
	"github.com/busovikov/takos/internal/tui"
	"golang.org/x/term"
)

// workspaceFlag is the persistent --workspace override (else $TAKOS_WORKSPACE,
// else the workspace containing the current directory).
var workspaceFlag string

// initRoot is where `task init` scaffolds: the explicit --workspace, else
// $TAKOS_WORKSPACE, else the current directory.
func initRoot() (string, error) {
	if workspaceFlag != "" {
		return workspaceFlag, nil
	}
	if v := os.Getenv("TAKOS_WORKSPACE"); v != "" {
		return v, nil
	}
	return os.Getwd()
}

// registerWorkspace puts a freshly created workspace into the registry so the
// daemon watches it and other directories can address it by name. Already
// registered (or an unusable name) is not an error worth failing init over.
func registerWorkspace(root string) {
	reg, err := registry.Load()
	if err != nil {
		warn("registry: %v", err)
		return
	}
	entry, err := reg.Add("", root)
	if err != nil {
		return // already registered — nothing to do
	}
	if err := reg.Save(); err != nil {
		warn("registry: %v", err)
		return
	}
	ok("workspace %q registered", entry.Name)
}

func openWS() (*core.Workspace, error) {
	ws, err := openWSRaw()
	if err != nil {
		return nil, err
	}
	registry.Touch(ws.Root) // keeps the picker ordered by recency
	catchUpSprints(ws)
	return ws, nil
}

// errPickAborted: the user left the workspace picker without choosing — a
// quiet exit, not a failure.
var errPickAborted = errors.New("no workspace picked")

// openWSOrPick answers "you are outside every workspace" on a TTY with the
// picker over the registry (most recently used first) instead of an error.
func openWSOrPick() (*core.Workspace, error) {
	ws, err := openWS()
	if err == nil || !errors.Is(err, registry.ErrNoWorkspace) ||
		!term.IsTerminal(int(os.Stdout.Fd())) {
		return ws, err
	}
	reg, lerr := registry.Load()
	if lerr != nil || len(reg.Workspaces) == 0 {
		return nil, err // nothing to pick from: the original hint is better
	}
	entry, perr := tui.Pick(reg.Entries())
	if perr != nil {
		return nil, perr
	}
	if entry.Name == "" {
		return nil, errPickAborted
	}
	ws, err = core.Open(entry.Root)
	if err != nil {
		return nil, err
	}
	registry.Touch(ws.Root)
	catchUpSprints(ws)
	return ws, nil
}

// openWSRaw opens the workspace without side effects. The --workspace value
// may be a registry name or a path; empty falls back to $TAKOS_WORKSPACE →
// the workspace containing the current directory.
func openWSRaw() (*core.Workspace, error) {
	root, err := registry.Resolve(workspaceFlag)
	if err != nil {
		return nil, err
	}
	return core.Open(root)
}

// catchUpSprints closes an overdue active sprint on plain CLI start — the
// scheduler's fallback when no daemon is around. When the daemon's instance
// holds the workspace lock it is its job; best-effort otherwise.
func catchUpSprints(ws *core.Workspace) {
	lock, err := fsstore.TryLock(filepath.Join(ws.Root, trackerd.WorkspaceLockFile))
	if err != nil {
		return // the daemon is running and keeps the schedule itself
	}
	defer lock.Unlock()
	rep := &core.ReconcileReport{}
	if err := ws.CatchUpSprints(time.Now(), rep); err != nil {
		warn("sprint catch-up: %v", err)
		return
	}
	for _, a := range rep.Actions {
		warn("%s", a)
	}
}

// ago renders a compact "5m ago" for tables and status lines.
func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func ok(format string, a ...any)   { fmt.Printf("✔ "+format+"\n", a...) }
func warn(format string, a ...any) { fmt.Fprintf(os.Stderr, "⚠ "+format+"\n", a...) }
