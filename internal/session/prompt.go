package session

import (
	"path/filepath"
	"strings"

	"github.com/busovikov/takos/internal/core"
)

// stageRole maps a stage to its interactive role by convention; the role's
// prompt from workflow.yaml becomes the session's opening instruction.
var stageRole = map[string]string{
	"refinement":     "context",
	"planning":       "planner",
	"implementation": "executor",
	"review":         "reviewer",
}

// RoleForStage returns the conventional role of a stage ("" — none).
func RoleForStage(stage string) string { return stageRole[stage] }

// genericPrompt opens a session when the stage has no configured role.
const genericPrompt = `You are working on task {{task.id}} "{{task.title}}".
Read {{task.dir}}/task.md (Objective, Context) before doing anything.`

// Prompt renders the opening prompt of a fresh session: the role's template
// from workflow.yaml (or a generic one) with {{…}} variables expanded.
func Prompt(ws *core.Workspace, task *core.Task, taskDir, role string) string {
	tpl := genericPrompt
	if role != "" {
		if r, ok := ws.WF.Roles[role]; ok && strings.TrimSpace(r.Prompt) != "" {
			tpl = r.Prompt
		}
	}
	round := ""
	if p, err := core.NewRoundPath(taskDir); err == nil {
		round = filepath.Base(p)
	}
	return strings.TrimSpace(Expand(tpl, map[string]string{
		"task.id":     task.Meta.ID,
		"task.title":  task.Meta.Title,
		"task.dir":    taskDir,
		"task.repo":   task.Meta.Repo,
		"promptExtra": "",
		"round":       round,
		"target":      "",
	}))
}

// Expand substitutes {{key}} tokens; unknown tokens are left as-is.
func Expand(tpl string, vars map[string]string) string {
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}
