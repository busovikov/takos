package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/core"
)

// workspaceFlag is the persistent --workspace override (else $TAKOS_WORKSPACE,
// else ~/.takos/workspace).
var workspaceFlag string

func openWS() (*core.Workspace, error) {
	root := workspaceFlag
	if root == "" {
		root = core.DefaultRoot()
	}
	return core.Open(root)
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
