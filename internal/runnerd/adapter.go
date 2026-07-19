// Package runnerd is the headless runner: adapters translate a role run
// into an agent CLI invocation, the job executor streams the transcript to
// a file in real time and classifies how the session ended. The daemon
// around it (:7433, API, idempotency) arrives in phase B3 — this package is
// the machinery both the daemon and the CLI file-fallback share.
package runnerd

import (
	"strings"

	"github.com/busovikov/takos/internal/session"
)

// Request carries everything an adapter needs to build a headless argv.
type Request struct {
	Bin            string // resolved agent binary
	Prompt         string
	UUID           string // deterministic session uuid (see internal/session)
	Resume         bool
	Model          string // role's model ("" — the agent's default)
	PermissionMode string // safe (read-only-ish) | auto (may edit and run)
}

// Adapter is one supported agent CLI in headless mode.
type Adapter interface {
	ID() string
	// Argv builds the full command line. The contract per agent is pinned
	// by fixture tests — flag drift shows up there first.
	Argv(r Request) []string
	// Classify names the outcome from the exit error and the transcript
	// tail: finished | limit | interrupted.
	Classify(exitErr error, transcript string) string
}

// ByID resolves a workflow agent id to its headless adapter.
func ByID(id string) (Adapter, bool) {
	switch id {
	case "claude-code":
		return claudeAdapter{}, true
	case "opencode":
		return opencodeAdapter{}, true
	}
	return nil, false
}

// classify is the shared skeleton: a clean exit is finished; a failure with
// a limit signature in the transcript is limit; anything else interrupted.
func classify(exitErr error, transcript string, limitMarks ...string) string {
	if exitErr == nil {
		return session.OutcomeFinished
	}
	low := strings.ToLower(transcript)
	for _, mark := range limitMarks {
		if strings.Contains(low, mark) {
			return session.OutcomeLimit
		}
	}
	return session.OutcomeInterrupted
}
