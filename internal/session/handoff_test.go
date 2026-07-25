package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/busovikov/takos/internal/core"
)

func handoffWS(t *testing.T) *core.Workspace {
	t.Helper()
	ws, err := core.Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// TestNeedHandoff pins the digest convention: neither plan-hand-off.md nor
// hand-off.md has anything to digest before the first session of their
// stage (the executor reads plan.md directly on its first pass), so both
// skip on "first" and only follow later sessions of the same stage. The
// stage role's handoff template is the switch.
func TestNeedHandoff(t *testing.T) {
	ws := handoffWS(t)
	cases := []struct {
		stage, reason string
		want          bool
		target        string
	}{
		{"planning", "first", false, ""},
		{"planning", "finished", true, core.PlanHandOffFile},
		{"planning", "interrupted", true, core.PlanHandOffFile},
		{"implementation", "first", false, ""},
		{"implementation", "finished", true, core.HandOffFile},
		{"refinement", "finished", false, ""},
		{"review", "finished", false, ""},
	}
	for _, tc := range cases {
		h, ok := NeedHandoff(ws, tc.stage, tc.reason)
		if ok != tc.want {
			t.Errorf("NeedHandoff(%s, %s) = %v, want %v", tc.stage, tc.reason, ok, tc.want)
			continue
		}
		if !ok {
			continue
		}
		if h.Target != tc.target {
			t.Errorf("%s/%s: target = %s, want %s", tc.stage, tc.reason, h.Target, tc.target)
		}
		if h.Stage != tc.stage+"-handoff" {
			t.Errorf("%s: pseudo-stage = %s", tc.stage, h.Stage)
		}
		if h.Template == "" {
			t.Errorf("%s: empty template", tc.stage)
		}
	}

	// No handoff template on the stage role — the summarizer stays off.
	r := ws.WF.Roles["planner"]
	r.Handoff = ""
	ws.WF.Roles["planner"] = r
	if _, ok := NeedHandoff(ws, "planning", "finished"); ok {
		t.Error("NeedHandoff must be off without a handoff template")
	}
}

// The pseudo-stage session name must not collide with the main stage's
// generation counting (prefix "T-XXXX-planning#").
func TestHandoffSessionsDoNotCountAsStageSessions(t *testing.T) {
	events := []core.HistoryEvent{
		{Actor: "agent:summarizer", Verb: "session", Args: "start T-0001-planning-handoff#1 x"},
		{Actor: "agent:summarizer", Verb: "session", Args: "finished T-0001-planning-handoff#1"},
	}
	p := PlanEntry(events, "T-0001", "planning")
	if p.Reason != "first" || p.NewGen != 1 {
		t.Errorf("digest sessions leaked into the stage generations: %+v", p)
	}
	ph := PlanEntry(events, "T-0001", "planning-handoff")
	if ph.Reason != "finished" || ph.NewGen != 2 {
		t.Errorf("digest generations broken: %+v", ph)
	}
}

func TestHandoffPrompt(t *testing.T) {
	ws := handoffWS(t)
	task := &core.Task{
		Dir:  "/ws/tasks/T-0001-x",
		Meta: core.TaskMeta{ID: "T-0001", Title: "X"},
	}
	h, ok := NeedHandoff(ws, "implementation", "finished")
	if !ok {
		t.Fatal("no handoff for implementation/finished")
	}
	got := HandoffPrompt(ws, task, h)
	for _, want := range []string{
		"/ws/tasks/T-0001-x/" + core.HandOffFile, // {{target}}
		"## Done", "## From review",              // {{template}}
		"T-0001",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("unexpanded tokens left:\n%s", got)
	}
}
