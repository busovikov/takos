// Package core is the tracker's heart: direct filesystem operations on the
// workspace (task folders, symlink handlers, history, workflow rules).
// It works without any daemon and is shared by the CLI, TUI and daemons.
package core

import "regexp"

var taskIDRe = regexp.MustCompile(`^T-\d{4,}$`)

// ValidTaskID reports whether s is a canonical task identifier (T-0001, T-12345, …).
func ValidTaskID(s string) bool {
	return taskIDRe.MatchString(s)
}
