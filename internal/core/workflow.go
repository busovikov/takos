package core

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// workflow.yaml — единственный конфиг workspace: стадии, роли, правила.

const WorkflowFile = "workflow.yaml"

// Backlog is the pseudo-stage used in transitions (handler in backlog/).
const Backlog = "backlog"

type Workflow struct {
	SchemaVersion int             `yaml:"schemaVersion"`
	Settings      Settings        `yaml:"settings"`
	Stages        []Stage         `yaml:"stages"` // порядок = числовые префиксы папок
	Transitions   []Transition    `yaml:"transitions"`
	Roles         map[string]Role `yaml:"roles"`
	Rules         []Rule          `yaml:"rules"`
}

type Settings struct {
	PollIntervalSec       int `yaml:"pollIntervalSec"`
	RuleBudgetPerTaskHour int `yaml:"ruleBudgetPerTaskPerHour"`
	SprintLengthDays      int `yaml:"sprintLengthDays"`
}

type Stage struct {
	ID       string   `yaml:"id"`
	Needs    []string `yaml:"needs,omitempty"`    // гейт входа: глоб файла | check:<имя>
	Scaffold []string `yaml:"scaffold,omitempty"` // файлы, создаваемые при входе
}

type Transition struct {
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Manual bool   `yaml:"manual"` // доступен человеку (Tab/mv); false — только правилам
}

type Role struct {
	Agent          string   `yaml:"agent"`
	Model          string   `yaml:"model,omitempty"`
	PermissionMode string   `yaml:"permissionMode,omitempty"` // safe | auto
	Prompt         string   `yaml:"prompt"`
	Handoff        string   `yaml:"handoff,omitempty"` // шаблон выжимки hand-off
	Outputs        []string `yaml:"outputs,omitempty"` // обязательные артефакты (глобы)
}

type Rule struct {
	ID    string       `yaml:"id"`
	On    string       `yaml:"on"` // "enter <stage>" | "session-finished" | "review" | "check <имя>"
	If    string       `yaml:"if,omitempty"`
	Spawn *SpawnAction `yaml:"spawn,omitempty"` // сахар для then: [{spawn: …}]
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
		add("нет ни одной стадии")
	}
	stages := map[string]bool{}
	for _, s := range w.Stages {
		if !stageIDRe.MatchString(s.ID) {
			add("стадия %q: id должен быть [a-z][a-z0-9-]*", s.ID)
		}
		if stages[s.ID] {
			add("стадия %q объявлена дважды", s.ID)
		}
		stages[s.ID] = true
		for _, n := range s.Needs {
			if strings.HasPrefix(n, "check:") && strings.TrimPrefix(n, "check:") == "" {
				add("стадия %q: пустое имя check в needs", s.ID)
			}
		}
	}
	if stages[Backlog] {
		add("%q — зарезервированное имя, стадией быть не может", Backlog)
	}

	stageOrBacklog := func(id string) bool { return stages[id] || id == Backlog }
	for _, t := range w.Transitions {
		if !stageOrBacklog(t.From) {
			add("переход %s -> %s: неизвестная стадия %q", t.From, t.To, t.From)
		}
		if !stageOrBacklog(t.To) {
			add("переход %s -> %s: неизвестная стадия %q", t.From, t.To, t.To)
		}
	}

	for name, r := range w.Roles {
		if r.Agent == "" {
			add("роль %q: не задан agent", name)
		}
		if strings.TrimSpace(r.Prompt) == "" {
			add("роль %q: пустой prompt", name)
		}
	}

	ruleIDs := map[string]bool{}
	for _, r := range w.Rules {
		if r.ID == "" {
			add("правило без id")
			continue
		}
		if ruleIDs[r.ID] {
			add("правило %q объявлено дважды", r.ID)
		}
		ruleIDs[r.ID] = true

		if err := validateRuleOn(r.On, stages); err != "" {
			add("правило %q: %s", r.ID, err)
		}
		actions := r.Then
		if r.Spawn != nil {
			actions = append(actions, Action{Spawn: r.Spawn})
		}
		if len(actions) == 0 {
			add("правило %q: нет действий", r.ID)
		}
		for _, a := range actions {
			n := 0
			if a.Move != "" {
				n++
				if !stages[a.Move] {
					add("правило %q: move в неизвестную стадию %q", r.ID, a.Move)
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
					add("правило %q: spawn без role", r.ID)
				} else if !strings.Contains(role, "{{") {
					if _, ok := w.Roles[role]; !ok {
						add("правило %q: spawn неизвестной роли %q", r.ID, role)
					}
				}
			}
			if n != 1 {
				add("правило %q: действие должно быть ровно одним из move|check|notify|spawn", r.ID)
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
			return fmt.Sprintf("on: enter неизвестной стадии %q", f[1])
		}
	case len(f) == 1 && (f[0] == "session-finished" || f[0] == "review"):
	case len(f) == 2 && f[0] == "check":
	default:
		return fmt.Sprintf("непонятное событие on: %q", on)
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
