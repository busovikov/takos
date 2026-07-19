package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HandlerPos describes where a task's handler symlink currently lives.
// The position IS the task's state: nothing else stores stage or sprint.
type HandlerPos struct {
	Path   string // absolute path of the symlink
	Area   string // backlog | sprint | archive
	Sprint string // sprint name (for sprint|archive)
	Stage  string // stage id; "backlog" for the backlog
}

const (
	AreaBacklog = "backlog"
	AreaSprint  = "sprint"
	AreaArchive = "archive"
)

// HandlerID extracts "T-0005" from a handler path ".../T-0005-slug".
func HandlerID(path string) string { return handlerID(path) }

// RelinkHandler is the exported form of relinkHandler for the daemon's
// rollback of invalid manual moves.
func RelinkHandler(oldPath, destDir, tasksDir string) error {
	return relinkHandler(oldPath, destDir, tasksDir)
}

// handlerID extracts "T-0005" from a handler path ".../T-0005-slug".
func handlerID(path string) string {
	base := filepath.Base(path)
	parts := strings.SplitN(base, "-", 3)
	if len(parts) < 2 {
		return base
	}
	return parts[0] + "-" + parts[1]
}

// stageFromDirName strips the numeric prefix: "3-implementation" → "implementation".
func stageFromDirName(name string) string {
	if i := strings.Index(name, "-"); i > 0 {
		return name[i+1:]
	}
	return name
}

// FindTaskDir locates the canonical task folder tasks/<id>-*.
func (ws *Workspace) FindTaskDir(id string) (string, error) {
	if !ValidTaskID(id) {
		return "", fmt.Errorf("invalid task id %q (expected T-0001…)", id)
	}
	matches, err := filepath.Glob(filepath.Join(ws.TasksDir(), id+"-*"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("task %s not found", id)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("task %s: multiple folders in tasks/ — fix manually: %v", id, matches)
	}
	return matches[0], nil
}

// FindHandler locates the task's handler symlink across backlog, sprints and
// archive.
func (ws *Workspace) FindHandler(id string) (*HandlerPos, error) {
	var found []HandlerPos

	if matches, _ := filepath.Glob(filepath.Join(ws.BacklogDir(), id+"-*")); len(matches) > 0 {
		for _, m := range matches {
			found = append(found, HandlerPos{Path: m, Area: AreaBacklog, Stage: Backlog})
		}
	}
	for _, area := range []struct{ root, kind string }{
		{ws.SprintsDir(), AreaSprint},
		{ws.ArchiveDir(), AreaArchive},
	} {
		matches, _ := filepath.Glob(filepath.Join(area.root, "*", "*", id+"-*"))
		for _, m := range matches {
			stageDir := filepath.Base(filepath.Dir(m))
			sprint := filepath.Base(filepath.Dir(filepath.Dir(m)))
			found = append(found, HandlerPos{
				Path: m, Area: area.kind, Sprint: sprint, Stage: stageFromDirName(stageDir),
			})
		}
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("handler of task %s not found (the reconciler will restore it)", id)
	case 1:
		return &found[0], nil
	default:
		return nil, fmt.Errorf("task %s: %d handlers found — invariant broken, the reconciler will repair it", id, len(found))
	}
}

// ListHandlers returns every handler in backlog + a given sprint (or all
// sprints when sprintName is empty). Archive is excluded.
func (ws *Workspace) ListHandlers(sprintName string) ([]HandlerPos, error) {
	var out []HandlerPos
	entries, err := os.ReadDir(ws.BacklogDir())
	if err == nil {
		for _, e := range entries {
			out = append(out, HandlerPos{
				Path: filepath.Join(ws.BacklogDir(), e.Name()), Area: AreaBacklog, Stage: Backlog,
			})
		}
	}
	sprints, err := ws.Sprints()
	if err != nil {
		return nil, err
	}
	for i := range sprints {
		if sprintName != "" && sprints[i].Name != sprintName {
			continue
		}
		hs, err := ws.sprintHandlers(&sprints[i])
		if err != nil {
			return nil, err
		}
		out = append(out, hs...)
	}
	return out, nil
}

// relinkHandler moves a handler symlink into destDir, recomputing the relative
// target: create-with-temp-name → rename(2) → unlink old. Manual `mv` that
// breaks the relative target is repaired by the watcher/reconciler instead.
func relinkHandler(oldPath, destDir, tasksDir string) error {
	name := filepath.Base(oldPath)
	taskDir := filepath.Join(tasksDir, name)
	target, err := filepath.Rel(destDir, taskDir)
	if err != nil {
		return err
	}
	tmp := filepath.Join(destDir, ".tmp-"+name)
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	newPath := filepath.Join(destDir, name)
	if err := os.Rename(tmp, newPath); err != nil {
		os.Remove(tmp)
		return err
	}
	if oldPath != newPath {
		os.Remove(oldPath)
	}
	return nil
}
