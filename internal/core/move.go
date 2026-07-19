package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MoveResult reports a successful transition for printing/history.
type MoveResult struct {
	ID   string
	From string
	To   string
}

// MoveTask validates and performs a stage transition of the task's handler.
// actor "user" goes through manual-transition rules; "engine"/"rule:<id>"
// may use automatic transitions too. destSprint chooses the target sprint
// when moving out of the backlog ("" → active).
//
// A refusal is an error carrying the exact phrase the TUI status bar shows;
// the filesystem is left untouched.
func (ws *Workspace) MoveTask(id, to, actor, destSprint string) (*MoveResult, error) {
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return nil, err
	}
	pos, err := ws.FindHandler(id)
	if err != nil {
		return nil, err
	}
	from := pos.Stage

	if err := ws.ValidateMove(id, taskDir, pos, to, actor); err != nil {
		return nil, err
	}

	// Target directory.
	destDir, err := ws.destDirFor(pos, to, destSprint)
	if err != nil {
		return nil, err
	}

	// Scaffold of the target stage (created before the move — crash-safe).
	if to != Backlog {
		ws.ScaffoldStage(taskDir, to)
	}

	if err := relinkHandler(pos.Path, destDir, ws.TasksDir()); err != nil {
		return nil, err
	}
	AppendHistory(taskDir, actor, "move", fmt.Sprintf("%s -> %s", from, to))
	return &MoveResult{ID: id, From: from, To: to}, nil
}

// ValidateMove checks every rule guarding a transition of the task's handler
// to stage `to` (or the backlog) without touching the filesystem. MoveTask and
// the daemon's manual-mv validation share it, so a Tab in the TUI, a CLI move
// and an external `mv` are judged by exactly the same rules.
func (ws *Workspace) ValidateMove(id, taskDir string, pos *HandlerPos, to, actor string) error {
	from := pos.Stage

	if pos.Area == AreaArchive {
		return fmt.Errorf("%s: task is archived (%s) — moves are not allowed", id, pos.Sprint)
	}
	if from == to {
		return fmt.Errorf("%s is already in %s", id, to)
	}

	// Untriaged review findings block every move.
	if n, err := OpenReviewCount(taskDir); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("%s: %s → %s — %d review findings still open (takos task review %s)",
			id, from, to, n, id)
	}

	if to == Backlog {
		// Any stage may go back to the backlog (a deliberate action,
		// confirmed in the TUI).
		return nil
	}
	if _, ok := ws.WF.StageIndex(to); !ok {
		return fmt.Errorf("unknown stage %q", to)
	}
	allowed, manual := ws.WF.TransitionAllowed(from, to)
	if !allowed {
		return fmt.Errorf("%s: transition %s → %s is not allowed by the workflow", id, from, to)
	}
	if actor == "user" && !manual {
		return fmt.Errorf("%s: transition %s → %s is automation-only", id, from, to)
	}
	// Entry gates (needs): a file glob inside the task folder or check:<name>.
	for _, need := range ws.stageByID(to).Needs {
		if err := ws.checkNeed(taskDir, need); err != nil {
			return fmt.Errorf("%s: %s → %s — %w", id, from, to, err)
		}
	}
	return nil
}

// ScaffoldStage creates the stage's scaffold files inside the task folder
// (empty, only when missing). Used on entry by MoveTask and by the daemon
// after accepting an external mv.
func (ws *Workspace) ScaffoldStage(taskDir, stage string) {
	for _, f := range ws.stageByID(stage).Scaffold {
		p := filepath.Join(taskDir, f)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			os.WriteFile(p, nil, 0o644)
		}
	}
}

func (ws *Workspace) stageByID(id string) *Stage {
	for i := range ws.WF.Stages {
		if ws.WF.Stages[i].ID == id {
			return &ws.WF.Stages[i]
		}
	}
	return &Stage{}
}

func (ws *Workspace) checkNeed(taskDir, need string) error {
	if name, ok := strings.CutPrefix(need, "check:"); ok {
		if !HasCheck(taskDir, name) {
			if name == "plan-resolved" {
				return fmt.Errorf("plan is not resolved (takos task resolve <id> once the plan is ready)")
			}
			return fmt.Errorf("requires check %s", name)
		}
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(taskDir, need))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Size() > 0 {
			return nil
		}
	}
	return fmt.Errorf("requires a non-empty %s", need)
}

func (ws *Workspace) destDirFor(pos *HandlerPos, to, destSprint string) (string, error) {
	if to == Backlog {
		return ws.BacklogDir(), nil
	}
	dn, _ := ws.WF.StageDirName(to)
	if pos.Area == AreaSprint && destSprint == "" {
		return filepath.Join(ws.SprintsDir(), pos.Sprint, dn), nil
	}
	var sprint *SprintInfo
	var err error
	if destSprint == "next" {
		sprint, err = ws.NextSprint()
	} else if destSprint != "" {
		sprint = &SprintInfo{Name: destSprint, Dir: filepath.Join(ws.SprintsDir(), destSprint)}
		if _, statErr := os.Stat(sprint.Dir); statErr != nil {
			err = fmt.Errorf("sprint %s not found", destSprint)
		}
	} else {
		sprint, err = ws.ActiveSprint()
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(sprint.Dir, dn), nil
}

// NextStage / PrevStage resolve Tab/⇧Tab targets relative to the current stage.
func (ws *Workspace) NextStage(from string) (string, bool) {
	i, ok := ws.WF.StageIndex(from)
	if !ok || i+1 >= len(ws.WF.Stages) {
		return "", false
	}
	return ws.WF.Stages[i+1].ID, true
}

func (ws *Workspace) PrevStage(from string) (string, bool) {
	i, ok := ws.WF.StageIndex(from)
	if !ok || i == 0 {
		return "", false
	}
	return ws.WF.Stages[i-1].ID, true
}
