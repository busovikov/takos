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
// Отказ — ошибка с той же фразой, что показывает статус-бар TUI; ФС при
// отказе не тронута.
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

	if pos.Area == AreaArchive {
		return nil, fmt.Errorf("%s: задача в архиве (%s) — перемещения запрещены", id, pos.Sprint)
	}
	if from == to {
		return nil, fmt.Errorf("%s уже в %s", id, to)
	}

	// Необработанные замечания ревью блокируют любые перемещения.
	if n, err := OpenReviewCount(taskDir); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, fmt.Errorf("%s: %s → %s — %d замечаний не обработано (takos task review %s)",
			id, from, to, n, id)
	}

	// Разрешён ли переход.
	if to == Backlog {
		// В беклог можно из любой стадии (осознанное действие с подтверждением в TUI).
	} else {
		if _, ok := ws.WF.StageIndex(to); !ok {
			return nil, fmt.Errorf("неизвестная стадия %q", to)
		}
		allowed, manual := ws.WF.TransitionAllowed(from, to)
		if !allowed {
			return nil, fmt.Errorf("%s: переход %s → %s не разрешён workflow", id, from, to)
		}
		if actor == "user" && !manual {
			return nil, fmt.Errorf("%s: переход %s → %s выполняется только автоматикой", id, from, to)
		}
		// Гейты входа (needs): глоб файла в папке задачи или check:<имя>.
		for _, need := range ws.stageByID(to).Needs {
			if err := ws.checkNeed(taskDir, need); err != nil {
				return nil, fmt.Errorf("%s: %s → %s — %w", id, from, to, err)
			}
		}
	}

	// Целевая папка.
	destDir, err := ws.destDirFor(pos, to, destSprint)
	if err != nil {
		return nil, err
	}

	// Скаффолд целевой стадии (создаётся до перемещения — крашу безопасно).
	if to != Backlog {
		for _, f := range ws.stageByID(to).Scaffold {
			p := filepath.Join(taskDir, f)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				os.WriteFile(p, nil, 0o644)
			}
		}
	}

	if err := relinkHandler(pos.Path, destDir, ws.TasksDir()); err != nil {
		return nil, err
	}
	AppendHistory(taskDir, actor, "move", fmt.Sprintf("%s -> %s", from, to))
	return &MoveResult{ID: id, From: from, To: to}, nil
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
			hint := name
			if name == "plan-resolved" {
				hint = "план не resolved (takos task resolve <id>, если план готов)"
			} else {
				hint = fmt.Sprintf("нужен check %s", name)
			}
			return fmt.Errorf("%s", hint)
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
	return fmt.Errorf("нужен непустой %s", need)
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
			err = fmt.Errorf("спринт %s не найден", destSprint)
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
