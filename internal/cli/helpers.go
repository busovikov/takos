package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/trackerd"
)

// workspaceFlag is the persistent --workspace override (else $TAKOS_WORKSPACE,
// else ~/.takos/workspace).
var workspaceFlag string

func openWS() (*core.Workspace, error) {
	ws, err := openWSRaw()
	if err != nil {
		return nil, err
	}
	catchUpSprints(ws)
	return ws, nil
}

// openWSRaw opens the workspace without side effects. The --workspace value
// may be a registry name or a path; empty falls back to $TAKOS_WORKSPACE →
// registry default → ~/.takos/workspace.
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

// currentRepo returns the git toplevel of cwd, or "" outside a repository.
func currentRepo() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
