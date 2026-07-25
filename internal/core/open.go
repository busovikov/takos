package core

import (
	"os/exec"
	"runtime"
)

// OpenWithSystem hands the file to whatever application the OS associates
// with it — the caller decides whether to Start (detached) or Run (wait).
func OpenWithSystem(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path)
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path)
	default:
		return exec.Command("xdg-open", path)
	}
}
