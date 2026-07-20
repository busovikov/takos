package trackerd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/core"
)

// scan is the instance's single reaction to "something changed": diff the
// handler positions against the previous snapshot to catch manual mv
// gestures (validate → accept or roll back), then let the reconciler repair
// whatever else is off. withSprints additionally runs the sprint scheduler
// (poll ticks only — a mid-mv burst should not close a sprint).
func (in *instance) scan(withSprints bool) {
	now := time.Now()
	cur := in.snapshot()
	actions := in.validateExternalMoves(in.snap, cur)

	var rep *core.ReconcileReport
	var err error
	if withSprints {
		rep, err = in.ws.Reconcile(now)
	} else {
		rep = &core.ReconcileReport{}
		err = in.ws.ReconcileHandlers(rep)
	}
	if err != nil {
		in.report([]string{fmt.Sprintf("reconcile failed: %v", err)})
	} else {
		actions = append(actions, rep.Actions...)
	}

	// The automation engine reacts to whatever the history now says:
	// entered stages, finished sessions, checks.
	if in.engine != nil {
		actions = append(actions, in.engine.Scan(in.ctx)...)
	}

	in.snap = in.snapshot()
	in.mu.Lock()
	in.lastScan = now
	in.mu.Unlock()
	in.report(actions)

	// A watcher-driven scan (withSprints=false) reflects a real filesystem
	// change; a poll tick pings only when it actually did something. Either
	// way the web UI just re-fetches — the ping carries no payload.
	if in.hub != nil && (!withSprints || len(actions) > 0) {
		in.hub.broadcast()
	}
}

// snapshot maps every handler name to its current position.
func (in *instance) snapshot() map[string]core.HandlerPos {
	out := map[string]core.HandlerPos{}
	handlers, err := in.ws.ListHandlers("")
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
func (in *instance) validateExternalMoves(old, cur map[string]core.HandlerPos) []string {
	var actions []string
	for name, np := range cur {
		op, known := old[name]
		if !known || (op.Stage == np.Stage && op.Sprint == np.Sprint) {
			continue
		}
		id := core.HandlerID(name)
		taskDir, err := in.ws.FindTaskDir(id)
		if err != nil {
			continue // orphan handler — the reconciler removes it
		}

		// A move takos itself performed (CLI, TUI, a rule) is already
		// validated and recorded; re-logging it as a manual mv would plant a
		// duplicate history event — and a second trigger for the rules
		// engine. Only gestures without a matching record are manual.
		if events, err := core.ReadHistory(taskDir); err == nil {
			if last, ok := core.LastMove(events); ok && last == np.Stage && op.Stage != np.Stage {
				continue
			}
		}

		if op.Stage == np.Stage {
			// Same stage, different sprint: repositioning, not a transition.
			core.AppendHistory(taskDir, "user", "sprint-move",
				fmt.Sprintf("%s -> %s (%s)", op.Sprint, np.Sprint, np.Stage))
			actions = append(actions, fmt.Sprintf("%s: manual mv %s → %s accepted", id, op.Sprint, np.Sprint))
			continue
		}

		if err := in.ws.ValidateMove(id, taskDir, &op, np.Stage, "user"); err != nil {
			if rbErr := in.rollback(&op, &np); rbErr != nil {
				actions = append(actions, fmt.Sprintf("%s: rollback failed: %v", id, rbErr))
				continue
			}
			core.AppendHistory(taskDir, "engine", "reject",
				fmt.Sprintf("%s -> %s: %v", op.Stage, np.Stage, err))
			actions = append(actions, fmt.Sprintf("%s: manual mv rejected — %v", id, err))
			continue
		}

		if np.Stage != core.Backlog {
			in.ws.ScaffoldStage(taskDir, np.Stage)
		}
		core.AppendHistory(taskDir, "user", "move",
			fmt.Sprintf("%s -> %s", op.Stage, np.Stage))
		actions = append(actions, fmt.Sprintf("%s: manual mv %s → %s accepted", id, op.Stage, np.Stage))
	}
	return actions
}

// rollback returns a handler to its previous position after an invalid mv.
func (in *instance) rollback(old, cur *core.HandlerPos) error {
	destDir := filepath.Dir(old.Path)
	if _, err := os.Stat(destDir); err != nil {
		return err
	}
	return core.RelinkHandler(cur.Path, destDir, filepath.Join(in.ws.Root, core.TasksDirName))
}
