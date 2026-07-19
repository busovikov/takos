package core

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// workflow.yaml is the single workspace config: stages, roles, rules.

const WorkflowFile = "workflow.yaml"

// Backlog is the pseudo-stage used in transitions (handler in backlog/).
const Backlog = "backlog"

type Workflow struct {
	SchemaVersion int             `yaml:"schemaVersion"`
	Settings      Settings        `yaml:"settings"`
	Stages        []Stage         `yaml:"stages"` // order = numeric folder prefixes
	Transitions   []Transition    `yaml:"transitions"`
	Roles         map[string]Role `yaml:"roles"`
	Rules         []Rule          `yaml:"rules"`
}

type Settings struct {
	PollIntervalSec       int `yaml:"pollIntervalSec"`
	RuleBudgetPerTaskHour int `yaml:"ruleBudgetPerTaskPerHour"`
	SprintLengthDays      int `yaml:"sprintLengthDays"`

	// Failure paths of headless sessions (0 → the engine's defaults).
	LimitRetryMinutes    int `yaml:"limitRetryMinutes,omitempty"`    // pause before resuming a limited session (15)
	SessionMaxRetries    int `yaml:"sessionMaxRetries,omitempty"`    // crash resumes before automation pauses (2)
	SessionRetryDelaySec int `yaml:"sessionRetryDelaySec,omitempty"` // pause before resuming a crashed session (30)
}

type Stage struct {
	ID       string   `yaml:"id"`
	Needs    []string `yaml:"needs,omitempty"`    // entry gate: file glob | check:<name>
	Scaffold []string `yaml:"scaffold,omitempty"` // files created on entry
}

type Transition struct {
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Manual bool   `yaml:"manual"` // available to humans (Tab/mv); false — rules only
}

type Role struct {
	Agent          string   `yaml:"agent"`
	Model          string   `yaml:"model,omitempty"`
	PermissionMode string   `yaml:"permissionMode,omitempty"` // safe | auto
	Prompt         string   `yaml:"prompt"`
	Handoff        string   `yaml:"handoff,omitempty"` // hand-off digest template
	Outputs        []string `yaml:"outputs,omitempty"` // required artifacts (globs)
}

type Rule struct {
	ID    string       `yaml:"id"`
	On    string       `yaml:"on"` // "enter <stage>" | "session-finished" | "review" | "check <name>"
	If    string       `yaml:"if,omitempty"`
	Spawn *SpawnAction `yaml:"spawn,omitempty"` // sugar for then: [{spawn: …}]
	Then  []Action     `yaml:"then,omitempty"`
}

type Action struct {
	Move   string       `yaml:"move,omitempty"`
	Check  string       `yaml:"check,omitempty"`
	Notify string       `yaml:"notify,omitempty"`
	Spawn  *SpawnAction `yaml:"spawn,omitempty"`
}

type SpawnAction struct {
	Role        string `yaml:"role"`
	PromptExtra string `yaml:"promptExtra,omitempty"`
	ResumeFrom  string `yaml:"resumeFrom,omitempty"`
	MaxRetries  int    `yaml:"maxRetries,omitempty"`
}

// ParseWorkflow unmarshals and validates workflow.yaml content.
func ParseWorkflow(data []byte) (*Workflow, error) {
	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("workflow.yaml: %w", err)
	}
	if errs := w.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("workflow.yaml: %s", strings.Join(errs, "; "))
	}
	return &w, nil
}

// LoadWorkflow reads and validates <workspace>/workflow.yaml.
func LoadWorkflow(workspace string) (*Workflow, error) {
	data, err := os.ReadFile(workspacePath(workspace, WorkflowFile))
	if err != nil {
		return nil, err
	}
	return ParseWorkflow(data)
}

func workspacePath(workspace string, parts ...string) string {
	return strings.Join(append([]string{workspace}, parts...), string(os.PathSeparator))
}

var stageIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Validate returns all consistency problems as human-readable messages
// (workflow validate prints every one, not just the first).
func (w *Workflow) Validate() []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if len(w.Stages) == 0 {
		add("no stages defined")
	}
	stages := map[string]bool{}
	for _, s := range w.Stages {
		if !stageIDRe.MatchString(s.ID) {
			add("stage %q: id must match [a-z][a-z0-9-]*", s.ID)
		}
		if stages[s.ID] {
			add("stage %q is declared twice", s.ID)
		}
		stages[s.ID] = true
		for _, n := range s.Needs {
			if strings.HasPrefix(n, "check:") && strings.TrimPrefix(n, "check:") == "" {
				add("stage %q: empty check name in needs", s.ID)
			}
		}
	}
	if stages[Backlog] {
		add("%q is a reserved name and cannot be a stage", Backlog)
	}

	stageOrBacklog := func(id string) bool { return stages[id] || id == Backlog }
	for _, t := range w.Transitions {
		if !stageOrBacklog(t.From) {
			add("transition %s -> %s: unknown stage %q", t.From, t.To, t.From)
		}
		if !stageOrBacklog(t.To) {
			add("transition %s -> %s: unknown stage %q", t.From, t.To, t.To)
		}
	}

	for name, r := range w.Roles {
		if r.Agent == "" {
			add("role %q: agent is not set", name)
		}
		if strings.TrimSpace(r.Prompt) == "" {
			add("role %q: empty prompt", name)
		}
	}

	ruleIDs := map[string]bool{}
	for _, r := range w.Rules {
		if r.ID == "" {
			add("rule without an id")
			continue
		}
		if ruleIDs[r.ID] {
			add("rule %q is declared twice", r.ID)
		}
		ruleIDs[r.ID] = true

		if err := validateRuleOn(r.On, stages); err != "" {
			add("rule %q: %s", r.ID, err)
		}
		actions := r.Then
		if r.Spawn != nil {
			actions = append(actions, Action{Spawn: r.Spawn})
		}
		if len(actions) == 0 {
			add("rule %q: no actions", r.ID)
		}
		for _, a := range actions {
			n := 0
			if a.Move != "" {
				n++
				if !stages[a.Move] {
					add("rule %q: move to unknown stage %q", r.ID, a.Move)
				}
			}
			if a.Check != "" {
				n++
			}
			if a.Notify != "" {
				n++
			}
			if a.Spawn != nil {
				n++
				role := a.Spawn.Role
				if role == "" {
					add("rule %q: spawn without a role", r.ID)
				} else if !strings.Contains(role, "{{") {
					if _, ok := w.Roles[role]; !ok {
						add("rule %q: spawn of unknown role %q", r.ID, role)
					}
				}
			}
			if n != 1 {
				add("rule %q: an action must be exactly one of move|check|notify|spawn", r.ID)
			}
		}
	}
	return errs
}

func validateRuleOn(on string, stages map[string]bool) string {
	f := strings.Fields(on)
	switch {
	case len(f) == 2 && f[0] == "enter":
		if !stages[f[1]] {
			return fmt.Sprintf("on: enter of unknown stage %q", f[1])
		}
	case len(f) == 1 && (f[0] == "session-finished" || f[0] == "review"):
	case len(f) == 2 && f[0] == "check":
	default:
		return fmt.Sprintf("unrecognized event on: %q", on)
	}
	return ""
}

// StageIndex returns the zero-based order of a stage (folder prefix N-1).
func (w *Workflow) StageIndex(id string) (int, bool) {
	for i, s := range w.Stages {
		if s.ID == id {
			return i, true
		}
	}
	return 0, false
}

// StageDirName is the on-disk folder name of a stage: "3-implementation".
func (w *Workflow) StageDirName(id string) (string, bool) {
	i, ok := w.StageIndex(id)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d-%s", i+1, id), true
}

// TransitionAllowed reports whether from→to exists and whether it is manual.
func (w *Workflow) TransitionAllowed(from, to string) (allowed, manual bool) {
	for _, t := range w.Transitions {
		if t.From == from && t.To == to {
			return true, t.Manual
		}
	}
	return false, false
}
