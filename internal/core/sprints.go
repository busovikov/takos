package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// SprintInfo couples a sprint folder with its parsed sprint.md.
type SprintInfo struct {
	Name string // "sprint-12"
	Dir  string
	Meta SprintMeta
}

var sprintNameRe = regexp.MustCompile(`^sprint-(\d+)$`)

// Sprints lists all sprints under sprints/, ordered by number.
func (ws *Workspace) Sprints() ([]SprintInfo, error) {
	entries, err := os.ReadDir(ws.SprintsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SprintInfo
	for _, e := range entries {
		if !e.IsDir() || !sprintNameRe.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(ws.SprintsDir(), e.Name())
		meta, err := LoadSprint(dir)
		if err != nil {
			continue // a sprint without a valid sprint.md is repaired by the reconciler
		}
		out = append(out, SprintInfo{Name: e.Name(), Dir: dir, Meta: *meta})
	}
	sort.Slice(out, func(i, j int) bool { return sprintNumber(out[i].Name) < sprintNumber(out[j].Name) })
	return out, nil
}

func sprintNumber(name string) int {
	m := sprintNameRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func (ws *Workspace) sprintByStatus(status string) (*SprintInfo, error) {
	sprints, err := ws.Sprints()
	if err != nil {
		return nil, err
	}
	for i := range sprints {
		if sprints[i].Meta.Status == status {
			return &sprints[i], nil
		}
	}
	return nil, fmt.Errorf("no sprint with status %q", status)
}

// ActiveSprint returns the current active sprint.
func (ws *Workspace) ActiveSprint() (*SprintInfo, error) { return ws.sprintByStatus(SprintActive) }

// NextSprint returns the always-existing next sprint.
func (ws *Workspace) NextSprint() (*SprintInfo, error) { return ws.sprintByStatus(SprintNext) }

// CreateSprint creates sprint-<max+1> with all stage folders. For an active
// sprint start dates are filled; a next sprint stays dateless until promotion.
func (ws *Workspace) CreateSprint(status string, start time.Time) (*SprintInfo, error) {
	sprints, err := ws.Sprints()
	if err != nil {
		return nil, err
	}
	max := 0
	for _, s := range sprints {
		if n := sprintNumber(s.Name); n > max {
			max = n
		}
	}
	name := fmt.Sprintf("sprint-%d", max+1)
	dir := filepath.Join(ws.SprintsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, s := range ws.WF.Stages {
		dn, _ := ws.WF.StageDirName(s.ID)
		if err := os.MkdirAll(filepath.Join(dir, dn), 0o755); err != nil {
			return nil, err
		}
	}
	days := ws.WF.Settings.SprintLengthDays
	if days <= 0 {
		days = DefaultSprintDays
	}
	meta := SprintMeta{Status: status, LengthDays: days}
	if status == SprintActive {
		meta.StartedAt = start
		meta.EndsAt = start.Add(time.Duration(days) * 24 * time.Hour)
	}
	if err := SaveSprint(dir, &meta); err != nil {
		return nil, err
	}
	return &SprintInfo{Name: name, Dir: dir, Meta: meta}, nil
}

// CloseActiveSprint closes the active sprint: unfinished handlers move to the
// next sprint preserving their stage, the whole sprint folder goes to
// archive/, the next sprint is promoted to active and a fresh next is created.
func (ws *Workspace) CloseActiveSprint(now time.Time) (closed, promoted string, carried int, err error) {
	active, err := ws.ActiveSprint()
	if err != nil {
		return "", "", 0, err
	}
	next, err := ws.NextSprint()
	if err != nil {
		return "", "", 0, err
	}
	lastStage := ws.WF.Stages[len(ws.WF.Stages)-1].ID

	// 1. Unfinished tasks → the next sprint, same stage.
	handlers, err := ws.sprintHandlers(active)
	if err != nil {
		return "", "", 0, err
	}
	for _, h := range handlers {
		if h.Stage == lastStage {
			continue // done stays and rides into the archive with the folder
		}
		destDir := filepath.Join(next.Dir, filepath.Base(filepath.Dir(h.Path)))
		if err := relinkHandler(h.Path, destDir, ws.TasksDir()); err != nil {
			return "", "", 0, err
		}
		if taskDir, err := ws.FindTaskDir(handlerID(h.Path)); err == nil {
			AppendHistory(taskDir, "engine", "sprint-carry",
				fmt.Sprintf("%s -> %s (%s)", active.Name, next.Name, h.Stage))
		}
		carried++
	}

	// 2. The whole sprint folder goes to the archive (same depth — symlinks stay valid).
	if err := os.MkdirAll(ws.ArchiveDir(), 0o755); err != nil {
		return "", "", 0, err
	}
	if err := os.Rename(active.Dir, filepath.Join(ws.ArchiveDir(), active.Name)); err != nil {
		return "", "", 0, err
	}

	// 3. The next sprint is promoted to active; a fresh next one is created.
	days := next.Meta.LengthDays
	if days <= 0 {
		days = DefaultSprintDays
	}
	next.Meta.Status = SprintActive
	next.Meta.StartedAt = now
	next.Meta.EndsAt = now.Add(time.Duration(days) * 24 * time.Hour)
	if err := SaveSprint(next.Dir, &next.Meta); err != nil {
		return "", "", 0, err
	}
	if _, err := ws.CreateSprint(SprintNext, time.Time{}); err != nil {
		return "", "", 0, err
	}
	return active.Name, next.Name, carried, nil
}

// sprintHandlers lists all handler symlinks inside one sprint's stage folders.
func (ws *Workspace) sprintHandlers(s *SprintInfo) ([]HandlerPos, error) {
	var out []HandlerPos
	for _, st := range ws.WF.Stages {
		dn, _ := ws.WF.StageDirName(st.ID)
		entries, err := os.ReadDir(filepath.Join(s.Dir, dn))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, HandlerPos{
				Path:   filepath.Join(s.Dir, dn, e.Name()),
				Area:   AreaSprint,
				Sprint: s.Name,
				Stage:  st.ID,
			})
		}
	}
	return out, nil
}
