package core

import (
	"strings"
	"testing"
)

const validWorkflow = `
schemaVersion: 1
settings: { pollIntervalSec: 5, ruleBudgetPerTaskPerHour: 20, sprintLengthDays: 14 }
stages:
  - id: refinement
  - id: planning
    scaffold: [plan.md]
  - id: implementation
    needs: ["check:plan-resolved"]
  - id: review
  - id: done
transitions:
  - { from: backlog,        to: refinement,     manual: true }
  - { from: refinement,     to: planning,       manual: true }
  - { from: refinement,     to: backlog,        manual: true }
  - { from: planning,       to: implementation, manual: true }
  - { from: implementation, to: review,         manual: true }
  - { from: review,         to: implementation, manual: false }
  - { from: review,         to: done,           manual: false }
roles:
  planner:
    agent: claude-code
    prompt: "Plan task {{task.id}}"
rules:
  - id: plan-on-planning
    on: enter planning
    spawn: { role: planner }
  - id: notify-review
    on: enter review
    then: [ { notify: "ready for review" } ]
`

func TestParseWorkflowValid(t *testing.T) {
	w, err := ParseWorkflow([]byte(validWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Stages) != 5 {
		t.Errorf("stages = %d, want 5", len(w.Stages))
	}
	if dir, ok := w.StageDirName("implementation"); !ok || dir != "3-implementation" {
		t.Errorf("StageDirName = %q, %v", dir, ok)
	}
	if allowed, manual := w.TransitionAllowed("refinement", "planning"); !allowed || !manual {
		t.Error("refinement -> planning must be allowed and manual")
	}
	if allowed, manual := w.TransitionAllowed("review", "done"); !allowed || manual {
		t.Error("review -> done must be allowed but not manual")
	}
	if allowed, _ := w.TransitionAllowed("done", "refinement"); allowed {
		t.Error("an absent transition must be forbidden")
	}
}

const brokenWorkflow = `
schemaVersion: 1
stages:
  - id: refinement
  - id: refinement
  - id: Bad_Name
transitions:
  - { from: refinement, to: nowhere, manual: true }
roles:
  planner:
    agent: ""
    prompt: ""
rules:
  - id: r1
    on: enter missing
    spawn: { role: ghost }
  - id: r1
    on: bogus event here
    then: [ { move: nowhere } ]
`

func TestParseWorkflowBroken(t *testing.T) {
	_, err := ParseWorkflow([]byte(brokenWorkflow))
	if err == nil {
		t.Fatal("a broken workflow must return an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"is declared twice",      // duplicate stage and rule
		"Bad_Name",               // id format
		"unknown stage",          // transition to nowhere
		"agent is not set",       // role
		"empty prompt",           // role
		"enter of unknown stage", // on
		"spawn of unknown role",  // spawn ghost
		"unrecognized event",     // on bogus
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}
