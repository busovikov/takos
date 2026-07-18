package core

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Workspace ties a workspace root to its parsed workflow. Everything the
// tracker does is a plain filesystem operation under this root.
type Workspace struct {
	Root string
	WF   *Workflow
}

const (
	TasksDirName   = "tasks"
	BacklogDirName = "backlog"
	SprintsDirName = "sprints"
	ArchiveDirName = "archive"
	ChecksDirName  = ".checks"
)

// DefaultRoot resolves the workspace path: $TAKOS_WORKSPACE or ~/.takos/workspace.
func DefaultRoot() string {
	if v := os.Getenv("TAKOS_WORKSPACE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".takos-workspace"
	}
	return filepath.Join(home, ".takos", "workspace")
}

// Open loads an existing workspace (workflow.yaml must exist and be valid).
func Open(root string) (*Workspace, error) {
	wf, err := LoadWorkflow(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace трекера не найден (%s)\nначни так: takos task init [--workspace DIR]", root)
	}
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: root, WF: wf}, nil
}

func (ws *Workspace) TasksDir() string   { return filepath.Join(ws.Root, TasksDirName) }
func (ws *Workspace) BacklogDir() string { return filepath.Join(ws.Root, BacklogDirName) }
func (ws *Workspace) SprintsDir() string { return filepath.Join(ws.Root, SprintsDirName) }
func (ws *Workspace) ArchiveDir() string { return filepath.Join(ws.Root, ArchiveDirName) }

// Init scaffolds a new workspace: directories, default workflow.yaml and the
// first active+next sprint pair. Idempotent: existing files are kept.
func Init(root string) (*Workspace, error) {
	for _, d := range []string{root, filepath.Join(root, TasksDirName),
		filepath.Join(root, BacklogDirName), filepath.Join(root, SprintsDirName),
		filepath.Join(root, ArchiveDirName)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	wfPath := filepath.Join(root, WorkflowFile)
	if _, err := os.Stat(wfPath); os.IsNotExist(err) {
		if err := os.WriteFile(wfPath, []byte(DefaultWorkflowYAML), 0o644); err != nil {
			return nil, err
		}
	}
	ws, err := Open(root)
	if err != nil {
		return nil, err
	}
	// Пара спринтов: активный и следующий.
	if _, err := ws.ActiveSprint(); err != nil {
		if _, err := ws.CreateSprint(SprintActive, time.Now()); err != nil {
			return nil, err
		}
	}
	if _, err := ws.NextSprint(); err != nil {
		if _, err := ws.CreateSprint(SprintNext, time.Time{}); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

// DefaultWorkflowYAML is the workflow scaffolded by `takos task init`.
const DefaultWorkflowYAML = `schemaVersion: 1

settings:
  pollIntervalSec: 5
  ruleBudgetPerTaskPerHour: 20
  sprintLengthDays: 14

stages:
  - id: refinement
  - id: planning
    scaffold: [plan.md]
  - id: implementation
    needs: ["check:plan-resolved"]
  - id: review
  - id: done

transitions:
  - { from: backlog,        to: refinement,     manual: true }   # взять в спринт
  - { from: refinement,     to: planning,       manual: true }
  - { from: planning,       to: implementation, manual: true }
  - { from: implementation, to: review,         manual: true }
  - { from: review,         to: implementation, manual: false }  # автодоработка по триажу
  - { from: review,         to: done,           manual: false }  # авто по триажу

roles:
  context:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Изучи репозиторий {{task.repo}} в контексте задачи {{task.id}} «{{task.title}}».
      Прочитай {{task.dir}}/task.md (Objective). Дополни task.md секцией "## Context":
      где в коде лежит относящееся к задаче, какие есть ограничения. Код не менять.
  planner:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Ты планируешь задачу {{task.id}} «{{task.title}}».
      Прочитай {{task.dir}}/task.md (Objective, Context) и {{task.dir}}/plan-hand-off.md,
      если он есть (решения прошлых сессий — не пересматривай их без причины).
      Составь пошаговый план и запиши в {{task.dir}}/plan.md. Код не менять. {{promptExtra}}
    outputs: [plan.md]
  executor:
    agent: claude-code
    permissionMode: auto
    prompt: |
      Реализуй задачу {{task.id}} по плану {{task.dir}}/plan.md.
      Сначала прочитай {{task.dir}}/hand-off.md (итог прошлой работы, секция «Из ревью»).
      Гоняй тесты. {{promptExtra}}
  handover:
    agent: claude-code
    permissionMode: auto
    prompt: |
      Передай задачу {{task.id}} в ревью. 1) Закоммить изменения в {{task.repo}}
      с заголовком "{{task.id}}: {{task.title}}" и подробным описанием в теле.
      2) Дополни {{task.dir}}/task.md секциями "## What is done", "## Docs",
      "## Testing" по фактически сделанному.
  reviewer:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Сделай ревью изменений задачи {{task.id}} в {{task.repo}}.
      Каждое замечание запиши в {{task.dir}}/reviews/{{round}} блоком
      "## [R<n>] <заголовок> — open" с описанием и диффом. Код не менять.
  summarizer:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Сожми материалы сессий задачи {{task.id}} в hand-off файл {{target}}.
      Секции по шаблону роли. Кратко, только проверенные факты.

rules:
  - id: context-on-planning
    on: enter planning
    spawn: { role: context }
  - id: handover-on-review
    on: enter review
    spawn: { role: handover }
  - id: review-after-handover
    on: session-finished
    if: role == handover && outcome == finished
    spawn: { role: reviewer }
`
