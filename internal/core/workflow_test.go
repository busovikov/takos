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
    then: [ { notify: "готово к ревью" } ]
`

func TestParseWorkflowValid(t *testing.T) {
	w, err := ParseWorkflow([]byte(validWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Stages) != 5 {
		t.Errorf("стадий %d, ожидалось 5", len(w.Stages))
	}
	if dir, ok := w.StageDirName("implementation"); !ok || dir != "3-implementation" {
		t.Errorf("StageDirName = %q, %v", dir, ok)
	}
	if allowed, manual := w.TransitionAllowed("refinement", "planning"); !allowed || !manual {
		t.Error("refinement -> planning должен быть разрешён и manual")
	}
	if allowed, manual := w.TransitionAllowed("review", "done"); !allowed || manual {
		t.Error("review -> done должен быть разрешён, но не manual")
	}
	if allowed, _ := w.TransitionAllowed("done", "refinement"); allowed {
		t.Error("отсутствующий переход должен быть запрещён")
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
		t.Fatal("битый workflow должен вернуть ошибку")
	}
	msg := err.Error()
	for _, want := range []string{
		"объявлена дважды",         // дубль стадии
		"Bad_Name",                 // формат id
		"неизвестная стадия",       // переход в nowhere
		"не задан agent",           // роль
		"пустой prompt",            // роль
		"enter неизвестной стадии", // on
		"неизвестной роли",         // spawn ghost
		"объявлено дважды",         // дубль правила
		"непонятное событие",       // on bogus
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в ошибке нет %q:\n%s", want, msg)
		}
	}
}
