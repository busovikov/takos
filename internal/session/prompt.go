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

// cliCheatSheet is appended to every prompt so an agent never has to
// discover takos by running --help or poking around: this is the whole
// surface it needs for moving its own task through the workflow.
const cliCheatSheet = `takos — the only commands you need for this task (don't explore for more):
  takos task move {{task.id}} <stage|next|prev|backlog>   move the task (next/prev = workflow order)
  takos task resolve {{task.id}}                          mark the plan ready (the gate into implementation)
  takos task review {{task.id}} [Rn] --resolve|--rework|--tech-debt   list, or triage one review finding
  takos task comment {{task.id}} "TEXT"                    add a review finding (review stage only)
  takos task logs {{task.id}} -f                           follow the task's current headless session until it ends
  takos task show {{task.id}} / history {{task.id}}        current state / audit trail`

// modeNote tells the agent whether anyone is watching, since that changes
// what "done with this stage" should do next.
func modeNote(headless bool) string {
	if headless {
		return `You are running headless: nobody reads anything you print here, and no reply is coming. Act on your own judgement — advance the task yourself with the commands above once your stage's work is done, instead of asking and waiting.`
	}
	return `You are running interactively: a human is watching this terminal. Once your stage's work is done, say so and propose the next step; if they agree, run the takos command yourself. If that move kicks off follow-up automation, run "takos task logs {{task.id}} -f" right after and wait for it to return before reporting the outcome and ending your turn.`
}

// Prompt renders the opening prompt of a fresh session: the role's template
// from workflow.yaml (or a generic one), the mode note and the CLI cheat
// sheet, with {{…}} variables expanded. extra fills {{promptExtra}} (a
// rule-supplied addition); headless marks a session with nobody watching.
func Prompt(ws *core.Workspace, task *core.Task, taskDir, role, extra string, headless bool) string {
	tpl := genericPrompt
	if role != "" {
		if r, ok := ws.WF.Roles[role]; ok && strings.TrimSpace(r.Prompt) != "" {
			tpl = r.Prompt
		}
	}
	full := tpl + "\n\n" + modeNote(headless) + "\n\n" + cliCheatSheet
	round := ""
	if p, err := core.NewRoundPath(taskDir); err == nil {
		round = filepath.Base(p)
	}
	return strings.TrimSpace(Expand(full, map[string]string{
		"task.id":     task.Meta.ID,
		"task.title":  task.Meta.Title,
		"task.dir":    taskDir,
		"task.repo":   task.Meta.Repo,
		"promptExtra": extra,
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
