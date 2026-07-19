package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The reconciler repairs the workspace invariants after anything that
// bypassed takos: manual mv/cp/rm of handlers, moves while the daemon was
// down, an overdue sprint. The handler's position stays the only authority
// on state; history.log is used solely to restore a handler that vanished
// and to pick the survivor among duplicates.

const QuarantineDirName = "quarantine"

// ReconcileReport lists every repair as a human-readable line; empty report
// means the workspace was consistent.
type ReconcileReport struct {
	Actions []string
}

func (r *ReconcileReport) addf(format string, a ...any) {
	r.Actions = append(r.Actions, fmt.Sprintf(format, a...))
}

// Reconcile runs every repair pass: sprint invariants (including overdue
// closes) and handler invariants. It is idempotent — a second run over a
// repaired workspace reports nothing.
func (ws *Workspace) Reconcile(now time.Time) (*ReconcileReport, error) {
	rep := &ReconcileReport{}
	if err := ws.reconcileSprints(now, rep); err != nil {
		return rep, err
	}
	if err := ws.ReconcileHandlers(rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// CatchUpSprints closes the overdue active sprint(s) — the scheduler path
// that also runs on plain CLI start when no daemon is around. Each close
// promotes the next sprint starting at the moment the previous one ended, so
// a long-idle workspace catches up whole cadence periods, not wall-clock now.
func (ws *Workspace) CatchUpSprints(now time.Time, rep *ReconcileReport) error {
	for range 100 { // hard cap: ~4 years of unattended 14-day sprints
		active, err := ws.ActiveSprint()
		if err != nil {
			return nil // no active sprint — reconcileSprints repairs that
		}
		ends := active.Meta.EndsAt
		if ends.IsZero() || now.Before(ends) {
			return nil
		}
		closed, promoted, carried, err := ws.CloseActiveSprint(ends)
		if err != nil {
			return err
		}
		rep.addf("sprint %s was overdue — closed to archive, %s is active now (%d tasks carried over)",
			closed, promoted, carried)
	}
	return nil
}

// reconcileSprints restores the "exactly one active + one next sprint"
// invariant and then catches up overdue closes.
func (ws *Workspace) reconcileSprints(now time.Time, rep *ReconcileReport) error {
	if _, err := ws.ActiveSprint(); err != nil {
		if next, nerr := ws.NextSprint(); nerr == nil {
			days := next.Meta.LengthDays
			if days <= 0 {
				days = DefaultSprintDays
			}
			next.Meta.Status = SprintActive
			next.Meta.StartedAt = now
			next.Meta.EndsAt = now.Add(time.Duration(days) * 24 * time.Hour)
			if err := SaveSprint(next.Dir, &next.Meta); err != nil {
				return err
			}
			rep.addf("no active sprint — promoted %s", next.Name)
		} else {
			s, err := ws.CreateSprint(SprintActive, now)
			if err != nil {
				return err
			}
			rep.addf("no active sprint — created %s", s.Name)
		}
	}
	if _, err := ws.NextSprint(); err != nil {
		s, err := ws.CreateSprint(SprintNext, time.Time{})
		if err != nil {
			return err
		}
		rep.addf("no next sprint — created %s", s.Name)
	}
	// Stage folders may be missing (workflow gained a stage, manual rmdir).
	sprints, err := ws.Sprints()
	if err != nil {
		return err
	}
	for _, s := range sprints {
		for _, st := range ws.WF.Stages {
			dn, _ := ws.WF.StageDirName(st.ID)
			dir := filepath.Join(s.Dir, dn)
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				rep.addf("%s: restored missing stage folder %s", s.Name, dn)
			}
		}
	}
	return ws.CatchUpSprints(now, rep)
}

// ReconcileHandlers walks every handler area and restores invariant 2
// ("exactly one handler in exactly one place") and invariant 4 (a handler is
// a symlink with a correct relative target). The daemon runs it alone on
// watcher bursts; Reconcile adds the sprint passes.
func (ws *Workspace) ReconcileHandlers(rep *ReconcileReport) error {
	handlers, err := ws.scanHandlerAreas(rep)
	if err != nil {
		return err
	}

	tasks, err := os.ReadDir(ws.TasksDir())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, t := range tasks {
		if !t.IsDir() || !taskDirRe.MatchString(t.Name()) {
			continue
		}
		name := t.Name()
		taskDir := filepath.Join(ws.TasksDir(), name)
		found := handlers[name]
		switch {
		case len(found) == 1:
			ws.syncHistory(taskDir, &found[0])
		case len(found) == 0:
			if err := ws.restoreHandler(taskDir, name, rep); err != nil {
				return err
			}
		default:
			if err := ws.dedupeHandlers(taskDir, name, found, rep); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanHandlerAreas lists backlog + sprint + archive stage folders, fixing
// what it can in place: non-symlinks go to quarantine, wrong relative
// targets are re-linked, orphans (no task folder) are removed. The returned
// map groups the surviving handlers by task folder name.
func (ws *Workspace) scanHandlerAreas(rep *ReconcileReport) (map[string][]HandlerPos, error) {
	out := map[string][]HandlerPos{}

	visit := func(dir string, pos HandlerPos) error {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == SprintFile {
				continue
			}
			p := filepath.Join(dir, name)
			h := pos
			h.Path = p

			fi, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				// cp -r instead of mv: content does not belong here.
				qp, err := ws.quarantine(p)
				if err != nil {
					return err
				}
				rep.addf("%s is not a handler symlink — quarantined to %s", ws.rel(p), ws.rel(qp))
				continue
			}
			taskDir := filepath.Join(ws.TasksDir(), name)
			if _, err := os.Stat(taskDir); err != nil {
				os.Remove(p)
				rep.addf("%s: task folder is gone — removed the orphan handler", ws.rel(p))
				continue
			}
			if !ws.targetCorrect(p, taskDir) {
				if err := relinkHandler(p, dir, ws.TasksDir()); err != nil {
					return err
				}
				rep.addf("%s: repaired the symlink target", ws.rel(p))
			}
			out[name] = append(out[name], h)
		}
		return nil
	}

	if err := visit(ws.BacklogDir(), HandlerPos{Area: AreaBacklog, Stage: Backlog}); err != nil {
		return nil, err
	}
	for _, area := range []struct{ root, kind string }{
		{ws.SprintsDir(), AreaSprint},
		{ws.ArchiveDir(), AreaArchive},
	} {
		sprints, err := os.ReadDir(area.root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, s := range sprints {
			if !s.IsDir() {
				continue
			}
			stageDirs, err := os.ReadDir(filepath.Join(area.root, s.Name()))
			if err != nil {
				return nil, err
			}
			for _, sd := range stageDirs {
				if !sd.IsDir() {
					continue
				}
				err := visit(filepath.Join(area.root, s.Name(), sd.Name()), HandlerPos{
					Area: area.kind, Sprint: s.Name(), Stage: stageFromDirName(sd.Name()),
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// restoreHandler recreates a vanished handler from history.log: the last
// recorded stage in the active sprint, or the backlog when history points
// there (or is silent).
func (ws *Workspace) restoreHandler(taskDir, name string, rep *ReconcileReport) error {
	events, _ := ReadHistory(taskDir)
	stage, _ := LastMove(events)

	destDir := ws.BacklogDir()
	where := Backlog
	if dn, ok := ws.WF.StageDirName(stage); ok {
		active, err := ws.ActiveSprint()
		if err != nil {
			return err
		}
		destDir = filepath.Join(active.Dir, dn)
		where = active.Name + "/" + stage
	}
	if err := relinkHandler(filepath.Join(destDir, name), destDir, ws.TasksDir()); err != nil {
		return err
	}
	AppendHistory(taskDir, "engine", "restore", where)
	rep.addf("%s: handler was missing — restored to %s", name, where)
	return nil
}

// dedupeHandlers keeps the handler matching the last history stage (sprint
// area wins ties) and removes the rest.
func (ws *Workspace) dedupeHandlers(taskDir, name string, found []HandlerPos, rep *ReconcileReport) error {
	events, _ := ReadHistory(taskDir)
	histStage, _ := LastMove(events)

	keep := 0
	best := -1
	for i, h := range found {
		score := 0
		if h.Stage == histStage {
			score += 2
		}
		if h.Area == AreaSprint {
			score++
		}
		if score > best {
			best, keep = score, i
		}
	}
	for i, h := range found {
		if i == keep {
			continue
		}
		os.Remove(h.Path)
		rep.addf("%s: duplicate handler %s removed (kept %s)",
			name, ws.rel(h.Path), ws.rel(found[keep].Path))
	}
	ws.syncHistory(taskDir, &found[keep])
	return nil
}

// syncHistory appends a move event when the handler's position disagrees
// with the last recorded one — moves done while the daemon was down become
// part of the chronicle (the position is fact; history follows it).
func (ws *Workspace) syncHistory(taskDir string, pos *HandlerPos) {
	events, _ := ReadHistory(taskDir)
	last, ok := LastMove(events)
	if !ok || last == pos.Stage {
		return
	}
	AppendHistory(taskDir, "engine", "move", fmt.Sprintf("%s -> %s", last, pos.Stage))
}

// targetCorrect reports whether the symlink resolves exactly to the task's
// canonical folder via a relative target.
func (ws *Workspace) targetCorrect(link, taskDir string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	if filepath.IsAbs(target) {
		return false // the workspace must stay relocatable
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(link), target))
	return resolved == filepath.Clean(taskDir)
}

// quarantine moves a foreign filesystem object into workspace/quarantine/.
func (ws *Workspace) quarantine(path string) (string, error) {
	qdir := filepath.Join(ws.Root, QuarantineDirName)
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(qdir, filepath.Base(path))
	for i := 2; ; i++ {
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			break
		}
		dest = filepath.Join(qdir, fmt.Sprintf("%s.%d", filepath.Base(path), i))
	}
	return dest, os.Rename(path, dest)
}

func (ws *Workspace) rel(p string) string {
	if r, err := filepath.Rel(ws.Root, p); err == nil {
		return r
	}
	return p
}
