package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/busovikov/takos/internal/fsstore"
)

// CreateOpts controls where and how a task is born.
type CreateOpts struct {
	Objective string // тело ## Objective (может быть пустым)
	Repo      string
	Priority  string // p0…p3; пусто → p2
	Backlog   bool   // хендлер в backlog/ вместо первой стадии активного спринта
	Sprint    string // "" → активный; "next" → следующий
}

var taskDirRe = regexp.MustCompile(`^T-(\d{4,})-`)

// NextTaskID scans tasks/ for the maximum existing number.
func (ws *Workspace) NextTaskID() (string, error) {
	entries, err := os.ReadDir(ws.TasksDir())
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	max := 0
	for _, e := range entries {
		if m := taskDirRe.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("T-%04d", max+1), nil
}

// CreateTask creates the canonical task folder and its handler symlink.
// The task exists as soon as this returns — the objective is an optional
// second stroke (SetObjective).
func (ws *Workspace) CreateTask(title string, opts CreateOpts) (*Task, *HandlerPos, error) {
	id, err := ws.NextTaskID()
	if err != nil {
		return nil, nil, err
	}
	name := id + "-" + Slugify(title)
	taskDir := filepath.Join(ws.TasksDir(), name)
	if err := os.Mkdir(taskDir, 0o755); err != nil {
		return nil, nil, err
	}

	prio := opts.Priority
	if prio == "" {
		prio = "p2"
	}
	task := &Task{
		Meta: TaskMeta{
			ID: id, Title: title, Repo: opts.Repo, Priority: prio,
			Automation: "active", CreatedAt: time.Now().UTC(),
		},
		Body: "## Objective\n" + opts.Objective + "\n",
		Dir:  taskDir,
	}
	if err := task.Save(); err != nil {
		return nil, nil, err
	}

	// Хендлер: беклог или первая стадия выбранного спринта.
	var destDir, stage string
	if opts.Backlog {
		destDir, stage = ws.BacklogDir(), Backlog
	} else {
		var sprint *SprintInfo
		if opts.Sprint == "next" {
			sprint, err = ws.NextSprint()
		} else {
			sprint, err = ws.ActiveSprint()
		}
		if err != nil {
			return nil, nil, err
		}
		stage = ws.WF.Stages[0].ID
		dn, _ := ws.WF.StageDirName(stage)
		destDir = filepath.Join(sprint.Dir, dn)
	}
	if err := relinkHandler(filepath.Join(destDir, name), destDir, ws.TasksDir()); err != nil {
		return nil, nil, err
	}
	AppendHistory(taskDir, "user", "created", stage)

	pos, err := ws.FindHandler(id)
	if err != nil {
		return nil, nil, err
	}
	return task, pos, nil
}

// SetObjective replaces the ## Objective section body of task.md.
func (t *Task) SetObjective(text string) error {
	t.Body = SetSection(t.Body, "Objective", text)
	return t.Save()
}

// --- checks: файлы-маркеры .checks/<имя> (существует = true) ---

// SetCheck creates the marker file for a named check.
func SetCheck(taskDir, name string) error {
	dir := filepath.Join(taskDir, ChecksDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fsstore.WriteFileAtomic(filepath.Join(dir, name), []byte{}, 0o644)
}

// RemoveCheck deletes the marker; missing is not an error.
func RemoveCheck(taskDir, name string) error {
	err := os.Remove(filepath.Join(taskDir, ChecksDirName, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// HasCheck reports whether the marker exists.
func HasCheck(taskDir, name string) bool {
	_, err := os.Stat(filepath.Join(taskDir, ChecksDirName, name))
	return err == nil
}
