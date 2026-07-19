package session

import (
	"reflect"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
)

// TestUUIDGolden pins the uuidv5 derivation forever: a change in namespace
// or name format would orphan every existing agent session — these exact
// values must never drift.
func TestUUIDGolden(t *testing.T) {
	golden := map[string]string{
		"T-0005-planning#1":       "8b0734d3-143c-561b-af4a-d35516026c9f",
		"T-0005-planning#2":       "bbc41fa8-e432-5b68-a249-290568c64299",
		"T-0005-implementation#1": "9d14706d-6739-5fc9-99df-e475498a1969",
	}
	for name, want := range golden {
		if got := UUID(name); got != want {
			t.Errorf("UUID(%s) = %s, want %s", name, got, want)
		}
	}
	if UUID("T-0005-planning#1") != UUID("T-0005-planning#1") {
		t.Error("UUID is not deterministic")
	}
}

func sessEvent(args string) core.HistoryEvent {
	return core.HistoryEvent{At: time.Now(), Actor: "user", Verb: "session", Args: args}
}

func TestPlanEntryPolicy(t *testing.T) {
	id, stage := "T-0005", "planning"
	cases := []struct {
		name   string
		events []core.HistoryEvent
		mode   Mode
		reason string
		gen    int    // expected NewGen (when a fresh session is on the table)
		resume string // expected ResumeName ("" — none)
	}{
		{"first session ever", nil, ModeNew, "first", 1, ""},
		{"finished → new generation",
			[]core.HistoryEvent{
				sessEvent("start T-0005-planning#1 x"),
				sessEvent("finished T-0005-planning#1"),
			},
			ModeNew, "finished", 2, ""},
		{"limit → resume, no choice",
			[]core.HistoryEvent{
				sessEvent("start T-0005-planning#1 x"),
				sessEvent("limit T-0005-planning#1"),
			},
			ModeResume, "limit", 0, "T-0005-planning#1"},
		{"interrupted → ask continue/new",
			[]core.HistoryEvent{
				sessEvent("start T-0005-planning#1 x"),
				sessEvent("interrupted T-0005-planning#1"),
			},
			ModeAsk, "interrupted", 2, "T-0005-planning#1"},
		{"crash: start without terminal → ask",
			[]core.HistoryEvent{sessEvent("start T-0005-planning#1 x")},
			ModeAsk, "interrupted", 2, "T-0005-planning#1"},
		{"resume after limit then finished → new gen 2",
			[]core.HistoryEvent{
				sessEvent("start T-0005-planning#1 x"),
				sessEvent("limit T-0005-planning#1"),
				sessEvent("resume T-0005-planning#1 x"),
				sessEvent("finished T-0005-planning#1"),
			},
			ModeNew, "finished", 2, ""},
		{"another stage's sessions are invisible",
			[]core.HistoryEvent{
				sessEvent("start T-0005-implementation#3 x"),
				sessEvent("limit T-0005-implementation#3"),
			},
			ModeNew, "first", 1, ""},
		{"generations count from the maximum",
			[]core.HistoryEvent{
				sessEvent("start T-0005-planning#1 x"),
				sessEvent("finished T-0005-planning#1"),
				sessEvent("start T-0005-planning#2 x"),
				sessEvent("finished T-0005-planning#2"),
			},
			ModeNew, "finished", 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PlanEntry(tc.events, id, stage)
			if p.Mode != tc.mode || p.Reason != tc.reason {
				t.Fatalf("mode/reason = %v/%s, want %v/%s", p.Mode, p.Reason, tc.mode, tc.reason)
			}
			if tc.gen > 0 {
				if p.NewGen != tc.gen || p.NewName != Name(id, stage, tc.gen) {
					t.Errorf("new = %s (#%d), want gen %d", p.NewName, p.NewGen, tc.gen)
				}
				if p.NewUUID != UUID(p.NewName) {
					t.Errorf("NewUUID mismatch")
				}
			}
			if p.ResumeName != tc.resume {
				t.Errorf("resume = %q, want %q", p.ResumeName, tc.resume)
			}
			if tc.resume != "" && p.ResumeUUID != UUID(tc.resume) {
				t.Errorf("ResumeUUID mismatch")
			}
		})
	}
}

// TestArgvFixtures pins the exact claude CLI contract: first entry uses
// --session-id with the prompt, continuation uses --resume. The risk list
// names flag drift explicitly — this is the fixture that catches it.
func TestArgvFixtures(t *testing.T) {
	uuid := "8b0734d3-143c-561b-af4a-d35516026c9f"
	if got, want := NewArgv("claude", uuid, "do the thing"),
		[]string{"claude", "--session-id", uuid, "do the thing"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NewArgv = %q, want %q", got, want)
	}
	if got, want := ResumeArgv("claude", uuid),
		[]string{"claude", "--resume", uuid}; !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgv = %q, want %q", got, want)
	}
}

func TestExpand(t *testing.T) {
	got := Expand("task {{task.id}}: {{task.title}} ({{missing}})", map[string]string{
		"task.id": "T-0001", "task.title": "X",
	})
	if got != "task T-0001: X ({{missing}})" {
		t.Errorf("Expand = %q", got)
	}
}

func TestRoleForStage(t *testing.T) {
	if RoleForStage("planning") != "planner" || RoleForStage("done") != "" {
		t.Error("stage→role convention broken")
	}
}
