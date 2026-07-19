// Package session implements named stage sessions: the pair "task + stage"
// maps to a deterministic native agent session `T-0005-<stage>#<generation>`.
// The UUID is uuidv5 of the name, the generation is counted from
// history.log, and the entry policy decides between `--session-id` (fresh)
// and `--resume` (continue). Everything here is derived from files — no
// session state of takos' own beyond the marker files.
package session

import (
	"crypto/sha1"
	"fmt"
	"strconv"
	"strings"

	"github.com/busovikov/takos/internal/core"
)

// namespace is the uuidv5 namespace of takos session names. Frozen forever:
// changing a single byte would orphan every existing agent session.
var namespace = [16]byte{
	0x8e, 0x77, 0x01, 0xae, 0x5a, 0x35, 0x4c, 0x02,
	0xa7, 0x1a, 0x3e, 0x1e, 0x0d, 0x1e, 0x5a, 0x5e,
}

// Name renders the session name: "T-0005-planning#2".
func Name(taskID, stage string, gen int) string {
	return fmt.Sprintf("%s-%s#%d", taskID, stage, gen)
}

// StageOf extracts the stage part of a session name ("" — not this task's
// name). Pseudo-stages come back verbatim: "planning-handoff".
func StageOf(taskID, name string) string {
	rest, ok := strings.CutPrefix(name, taskID+"-")
	if !ok {
		return ""
	}
	stage, _, ok := strings.Cut(rest, "#")
	if !ok {
		return ""
	}
	return stage
}

// UUID derives the deterministic session UUID (uuidv5) from a session name.
func UUID(name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum)
	u[6] = u[6]&0x0f | 0x50 // version 5
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// Session outcomes as recorded in history.log and markers.
const (
	OutcomeFinished    = "finished"
	OutcomeInterrupted = "interrupted"
	OutcomeLimit       = "limit"
)

// Entry modes of the policy.
type Mode int

const (
	ModeNew    Mode = iota // fresh session with --session-id
	ModeResume             // --resume is the only way (limit)
	ModeAsk                // interrupted: the user picks continue/new
)

// EntryPlan is the policy's verdict for entering a stage session.
type EntryPlan struct {
	Mode   Mode
	Reason string // first | finished | limit | interrupted

	// The fresh session (ModeNew, or ModeAsk resolved to "new").
	NewName string
	NewUUID string
	NewGen  int

	// The session to continue (ModeResume, or ModeAsk resolved to "continue").
	ResumeName string
	ResumeUUID string
}

// PlanEntry folds history.log into the entry decision for one task+stage:
// limit → always resume; interrupted (including a start with no terminal
// record — a crash) → ask; finished or no sessions yet → a fresh session.
func PlanEntry(events []core.HistoryEvent, taskID, stage string) EntryPlan {
	last := stageSessions(events, taskID, stage)
	maxGen := 0
	for g := range last {
		if g > maxGen {
			maxGen = g
		}
	}

	fresh := func(gen int, reason string) EntryPlan {
		name := Name(taskID, stage, gen)
		return EntryPlan{
			Mode: ModeNew, Reason: reason,
			NewName: name, NewUUID: UUID(name), NewGen: gen,
		}
	}
	if maxGen == 0 {
		return fresh(1, "first")
	}

	name := Name(taskID, stage, maxGen)
	switch last[maxGen] {
	case OutcomeLimit:
		return EntryPlan{
			Mode: ModeResume, Reason: OutcomeLimit,
			ResumeName: name, ResumeUUID: UUID(name),
		}
	case OutcomeFinished:
		return fresh(maxGen+1, OutcomeFinished)
	default: // interrupted, or start/resume without a terminal record
		p := fresh(maxGen+1, OutcomeInterrupted)
		p.Mode = ModeAsk
		p.ResumeName, p.ResumeUUID = name, UUID(name)
		return p
	}
}

// stageSessions maps generation → the last recorded session event
// ("start"/"resume" mean no terminal record yet) for one task+stage.
// History lines look like:
//
//	session start T-0005-planning#1 <uuid>
//	session finished T-0005-planning#1
func stageSessions(events []core.HistoryEvent, taskID, stage string) map[int]string {
	prefix := taskID + "-" + stage + "#"
	out := map[int]string{}
	for _, e := range events {
		if e.Verb != "session" {
			continue
		}
		f := strings.Fields(e.Args)
		if len(f) < 2 || !strings.HasPrefix(f[1], prefix) {
			continue
		}
		gen, err := strconv.Atoi(f[1][len(prefix):])
		if err != nil || gen <= 0 {
			continue
		}
		out[gen] = f[0]
	}
	return out
}

// NewArgv is the fixed CLI contract for a fresh session. Pinned by fixture
// tests: if the claude CLI ever changes these flags, the fixtures scream.
func NewArgv(bin, uuid, prompt string) []string {
	return []string{bin, "--session-id", uuid, prompt}
}

// ResumeArgv is the fixed CLI contract for continuing a session.
func ResumeArgv(bin, uuid string) []string {
	return []string{bin, "--resume", uuid}
}
