package session

import (
	"path/filepath"
	"strings"

	"github.com/busovikov/takos/internal/core"
)

// The hand-off digest is a stage's memory: plan-hand-off.md keeps planning
// decisions, hand-off.md keeps implementation progress. The summarizer role
// regenerates the digest before every fresh stage session, so a new session
// starts from condensed facts instead of replaying old transcripts.

// SummarizerRole is the workflow role that condenses session materials
// into hand-off digests.
const SummarizerRole = "summarizer"

// stageHandoff pairs a stage with its digest: the file, the role whose
// `handoff:` template shapes the sections, and whether the very first
// session already has something to digest. Neither does: the executor
// reads plan.md directly on its first pass (nothing implementation-shaped
// exists yet to summarize), and plan-hand-off.md has nothing to say before
// the first planning session either. The digest only earns its keep once a
// prior session of the same stage has left something behind.
var stageHandoff = map[string]struct {
	target      string
	role        string
	seedOnFirst bool
}{
	"planning":       {core.PlanHandOffFile, "planner", false},
	"implementation": {core.HandOffFile, "executor", false},
}

// Handoff describes one due digest regeneration.
type Handoff struct {
	Stage    string // pseudo-stage naming the summarizer session: "<stage>-handoff"
	Target   string // digest file name inside the task folder
	Template string // section skeleton from the stage role's handoff: field
}

// NeedHandoff decides whether a fresh session of a stage must be preceded
// by regenerating the stage's hand-off digest. reason is the entry-policy
// reason of the fresh session ("first" | "finished" | "interrupted"). The
// stage role's handoff template is the on-switch: no template — no
// summarizer.
func NeedHandoff(ws *core.Workspace, stage, reason string) (Handoff, bool) {
	spec, ok := stageHandoff[stage]
	if !ok {
		return Handoff{}, false
	}
	tpl := strings.TrimSpace(ws.WF.Roles[spec.role].Handoff)
	if tpl == "" {
		return Handoff{}, false
	}
	if reason == "first" && !spec.seedOnFirst {
		return Handoff{}, false
	}
	return Handoff{
		Stage:    stage + "-handoff",
		Target:   spec.target,
		Template: tpl,
	}, true
}

// HandoffPrompt renders the summarizer's opening prompt for one digest:
// the role's template with {{target}} and {{template}} expanded on top of
// the usual task variables.
func HandoffPrompt(ws *core.Workspace, task *core.Task, h Handoff) string {
	tpl := ""
	if r, ok := ws.WF.Roles[SummarizerRole]; ok {
		tpl = r.Prompt
	}
	return strings.TrimSpace(Expand(tpl, map[string]string{
		"task.id":    task.Meta.ID,
		"task.title": task.Meta.Title,
		"task.dir":   task.Dir,
		"task.repo":  task.Meta.Repo,
		"target":     filepath.Join(task.Dir, h.Target),
		"template":   h.Template,
	}))
}
