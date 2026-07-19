package trackerd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/core"
)

// scan is the daemon's single reaction to "something changed": diff the
// handler positions against the previous snapshot to catch manual mv
// gestures (validate → accept or roll back), then let the reconciler repair
// whatever else is off. withSprints additionally runs the sprint scheduler
// (poll ticks only — a mid-mv burst should not close a sprint).
func (d *Daemon) scan(withSprints bool) {
	now := time.Now()
	cur := d.snapshot()
	actions := d.validateExternalMoves(d.snap, cur)

	var rep *core.ReconcileReport
	var err error
	if withSprints {
		rep, err = d.ws.Reconcile(now)
	} else {
		rep = &core.ReconcileReport{}
		err = d.ws.ReconcileHandlers(rep)
	}
	if err != nil {
		d.report([]string{fmt.Sprintf("reconcile failed: %v", err)})
	} else {
		actions = append(actions, rep.Actions...)
	}

	d.snap = d.snapshot()
	d.mu.Lock()
	d.lastScan = now
	d.mu.Unlock()
	d.report(actions)
}

// snapshot maps every handler name to its current position.
func (d *Daemon) snapshot() map[string]core.HandlerPos {
	out := map[string]core.HandlerPos{}
	handlers, err := d.ws.ListHandlers("")
	if err != nil {
		return out
	}
	for _, h := range handlers {
		name := filepath.Base(h.Path)
		if strings.HasPrefix(name, ".") {
			continue
		}
		out[name] = h
	}
	return out
}

// validateExternalMoves compares two snapshots and pushes every detected
// manual mv through the same validator the TUI and CLI use. An invalid move
// is rolled back to its previous position with a "reject" history line —
// exactly the promise of the mv gesture: the filesystem is the UI, but the
// workflow still holds.
func (d *Daemon) validateExternalMoves(old, cur map[string]core.HandlerPos) []string {
	var actions []string
	for name, np := range cur {
		op, known := old[name]
		if !known || (op.Stage == np.Stage && op.Sprint == np.Sprint) {
			continue
		}
		id := core.HandlerID(name)
		taskDir, err := d.ws.FindTaskDir(id)
		if err != nil {
			continue // orphan handler — the reconciler removes it
		}

		if op.Stage == np.Stage {
			// Same stage, different sprint: repositioning, not a transition.
			core.AppendHistory(taskDir, "user", "sprint-move",
				fmt.Sprintf("%s -> %s (%s)", op.Sprint, np.Sprint, np.Stage))
			actions = append(actions, fmt.Sprintf("%s: manual mv %s → %s accepted", id, op.Sprint, np.Sprint))
			continue
		}

		if err := d.ws.ValidateMove(id, taskDir, &op, np.Stage, "user"); err != nil {
			if rbErr := d.rollback(&op, &np); rbErr != nil {
				actions = append(actions, fmt.Sprintf("%s: rollback failed: %v", id, rbErr))
				continue
			}
			core.AppendHistory(taskDir, "engine", "reject",
				fmt.Sprintf("%s -> %s: %v", op.Stage, np.Stage, err))
			actions = append(actions, fmt.Sprintf("%s: manual mv rejected — %v", id, err))
			continue
		}

		if np.Stage != core.Backlog {
			d.ws.ScaffoldStage(taskDir, np.Stage)
		}
		core.AppendHistory(taskDir, "user", "move",
			fmt.Sprintf("%s -> %s", op.Stage, np.Stage))
		actions = append(actions, fmt.Sprintf("%s: manual mv %s → %s accepted", id, op.Stage, np.Stage))
	}
	return actions
}

// rollback returns a handler to its previous position after an invalid mv.
func (d *Daemon) rollback(old, cur *core.HandlerPos) error {
	destDir := filepath.Dir(old.Path)
	if _, err := os.Stat(destDir); err != nil {
		return err
	}
	return core.RelinkHandler(cur.Path, destDir, filepath.Join(d.ws.Root, core.TasksDirName))
}
