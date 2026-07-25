package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
)

// A marker file tasks/<id>/sessions/<name>.json mirrors one session's
// lifecycle: pid while running, outcome once done. The board's ▶ indicator
// and `x`/cancel read markers; history.log stays the chronicle.

func init() {
	// core.MoveTask refuses to move a task to done while a session is
	// running; core can't import session (cycle), so wire it here.
	core.ActiveSessionChecker = func(taskDir string) bool { return Active(taskDir) != nil }
}

const SessionsDirName = "sessions"

type Marker struct {
	Name      string    `json:"name"`
	UUID      string    `json:"uuid"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
	Outcome   string    `json:"outcome,omitempty"`
}

// Running reports whether the marker describes a live session.
func (m *Marker) Running() bool {
	return m.EndedAt.IsZero() && alive(m.PID)
}

func markerPath(taskDir, name string) string {
	return filepath.Join(taskDir, SessionsDirName, name+".json")
}

func writeMarker(taskDir string, m *Marker) error {
	if err := os.MkdirAll(filepath.Join(taskDir, SessionsDirName), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsstore.WriteFileAtomic(markerPath(taskDir, m.Name), data, 0o644)
}

// Markers lists a task's session markers ordered by start time.
func Markers(taskDir string) []Marker {
	entries, err := os.ReadDir(filepath.Join(taskDir, SessionsDirName))
	if err != nil {
		return nil
	}
	var out []Marker
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(taskDir, SessionsDirName, e.Name()))
		if err != nil {
			continue
		}
		var m Marker
		if json.Unmarshal(data, &m) == nil && m.Name != "" {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Active returns the live session of a task, nil when there is none.
func Active(taskDir string) *Marker {
	for _, m := range Markers(taskDir) {
		if m.Running() {
			return &m
		}
	}
	return nil
}

// RepairStale finalizes markers whose process died without a terminal
// record (takos was killed mid-session): outcome interrupted, so the entry
// policy offers continue/new instead of believing a ghost is running.
func RepairStale(taskDir string) {
	for _, m := range Markers(taskDir) {
		if !m.EndedAt.IsZero() || alive(m.PID) {
			continue
		}
		m.EndedAt = time.Now()
		m.Outcome = OutcomeInterrupted
		if writeMarker(taskDir, &m) == nil {
			core.AppendHistory(taskDir, "engine", "session",
				OutcomeInterrupted+" "+m.Name)
		}
	}
}

// alive probes a pid with signal 0 (EPERM still means the process exists).
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
