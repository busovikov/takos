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
		return nil, fmt.Errorf("tracker workspace not found (%s)\nget started: takos task init [--workspace DIR]", root)
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
	// The active + next sprint pair.
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
  limitRetryMinutes: 15   # resume a limit-hit session after this pause
  sessionMaxRetries: 2    # crash resumes before the task's automation pauses

stages:
  - id: refinement
  - id: planning
    scaffold: [plan.md]
  - id: implementation
    needs: ["check:plan-resolved"]
    scaffold: [hand-off.md]
  - id: review
  - id: done

transitions:
  - { from: backlog,        to: refinement,     manual: true }   # pull into a sprint
  - { from: refinement,     to: planning,       manual: true }
  - { from: planning,       to: refinement,     manual: true }   # step back
  - { from: planning,       to: implementation, manual: true }
  - { from: implementation, to: planning,       manual: true }   # step back
  - { from: implementation, to: review,         manual: true }
  - { from: review,         to: implementation, manual: false }  # auto rework after triage
  - { from: review,         to: done,           manual: false }  # auto after triage

roles:
  context:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Study the repository {{task.repo}} in the context of task {{task.id}}
      "{{task.title}}". Read {{task.dir}}/task.md (Objective). Add a
      "## Context" section to task.md: where the relevant code lives and
      which constraints apply. Do not modify any code.
  planner:
    agent: claude-code
    permissionMode: safe
    prompt: |
      You are planning task {{task.id}} "{{task.title}}".
      Read {{task.dir}}/task.md (Objective, Context) and, if present,
      {{task.dir}}/plan-hand-off.md (decisions from previous sessions — do
      not revisit them without a reason). Write a step-by-step plan to
      {{task.dir}}/plan.md. Do not modify any code. {{promptExtra}}
    outputs: [plan.md]
    handoff: |
      ## Decisions
      ## Rejected
      ## Open questions
  executor:
    agent: claude-code
    permissionMode: auto
    prompt: |
      Implement task {{task.id}} following {{task.dir}}/plan.md.
      First read {{task.dir}}/hand-off.md (previous progress, the
      "From review" section). Run the tests. {{promptExtra}}
    handoff: |
      ## Done
      ## Repo state
      ## Next
      ## From review
  handover:
    agent: claude-code
    permissionMode: auto
    prompt: |
      Hand task {{task.id}} over to review. 1) Commit the changes in
      {{task.repo}} titled "{{task.id}}: {{task.title}}" with a detailed
      body. 2) Update {{task.dir}}/task.md with "## What is done",
      "## Docs" and "## Testing" sections reflecting the actual work.
  reviewer:
    agent: claude-code
    permissionMode: safe
    prompt: |
      Review the changes of task {{task.id}} in {{task.repo}}.
      Write every finding to {{task.dir}}/reviews/{{round}} as a block
      "## [R<n>] <title> — open" with a description and a diff. Do not
      modify any code.
  summarizer:
    agent: claude-code
    permissionMode: auto
    prompt: |
      Regenerate the hand-off digest of task {{task.id}} "{{task.title}}".
      Rewrite {{target}} from the materials in {{task.dir}}: session
      transcripts under sessions/, the artifacts (plan.md, reviews/),
      history.log and the current digest content. Keep exactly this
      section skeleton:

      {{template}}

      Be brief: verified facts only, no speculation. Do not touch any
      other file.

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
