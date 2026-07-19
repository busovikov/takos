package trackerd

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/rules"
	"github.com/busovikov/takos/internal/watch"
)

// Instance states.
const (
	StateRunning     = "running"
	StateUnavailable = "unavailable" // root missing/invalid/locked; retried on a tick
	StateStopped     = "stopped"
)

// instance is the per-workspace unit of the daemon: watcher + snapshot +
// reconciler + sprint scheduler over one workspace root. Instances are fully
// independent — a broken or slow workspace never stalls the others.
type instance struct {
	name string
	root string
	log  *log.Logger

	// Owned by the loop goroutine; no locking needed.
	ws     *core.Workspace
	snap   map[string]core.HandlerPos
	engine *rules.Engine
	ctx    context.Context // loop context, also bounds engine-spawned jobs

	mu        sync.Mutex
	state     string
	reason    string // why unavailable
	startedAt time.Time
	lastScan  time.Time
	recent    []string // last repair/validation actions, newest last

	lock   *fsstore.Lock
	cancel context.CancelFunc
	done   chan struct{}
}

func newInstance(name, root string, lg *log.Logger) *instance {
	return &instance{name: name, root: root, log: lg, state: StateUnavailable}
}

// tryStart brings the instance up: open the workspace, take its flock, run
// the initial reconciliation and launch the loop. Any failure parks the
// instance in the unavailable state — the supervisor retries on a tick.
func (in *instance) tryStart(ctx context.Context) {
	ws, err := core.Open(in.root)
	if err != nil {
		in.setUnavailable(fmt.Sprintf("cannot open: %v", err))
		return
	}
	lock, err := fsstore.TryLock(filepath.Join(in.root, WorkspaceLockFile))
	if err != nil {
		in.setUnavailable("workspace lock is held by another process")
		return
	}
	rep, err := ws.Reconcile(time.Now())
	if err != nil {
		lock.Unlock()
		in.setUnavailable(fmt.Sprintf("reconcile failed: %v", err))
		return
	}
	w, err := watch.New(in.root, debounce)
	if err != nil {
		lock.Unlock()
		in.setUnavailable(fmt.Sprintf("watcher: %v", err))
		return
	}

	in.ws = ws
	in.lock = lock
	in.snap = in.snapshot()
	ictx, cancel := context.WithCancel(ctx)
	in.cancel = cancel
	in.ctx = ictx
	in.engine = rules.New(ws, in.name, in.log, nil)
	in.done = make(chan struct{})

	in.mu.Lock()
	in.state, in.reason, in.startedAt = StateRunning, "", time.Now()
	in.mu.Unlock()
	in.report(rep.Actions)
	in.log.Printf("[%s] watching %s", in.name, in.root)

	go in.loop(ictx, w)
}

func (in *instance) loop(ctx context.Context, w *watch.Watcher) {
	defer close(in.done)
	defer w.Close()
	defer in.lock.Unlock()

	poll := time.Duration(in.ws.WF.Settings.PollIntervalSec) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	// One scan right away: automation that came due while the daemon was
	// down (entered stages, finished sessions) must not wait for a tick.
	in.scan(false)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-w.C:
			if !ok {
				in.setUnavailable("watcher closed unexpectedly")
				return
			}
			in.scan(false)
		case <-ticker.C:
			// The poll tick is the fallback for missed fsnotify events and
			// the sprint scheduler's clock.
			in.scan(true)
		}
	}
}

// stop shuts the loop down and releases the workspace lock. Engine-spawned
// in-process jobs share the loop context — cancelled here, awaited so their
// outcomes are recorded before the instance reports stopped.
func (in *instance) stop() {
	if in.cancel != nil {
		in.cancel()
		<-in.done
		in.cancel = nil
		if in.engine != nil {
			in.engine.Wait()
		}
	}
	in.mu.Lock()
	in.state = StateStopped
	in.mu.Unlock()
}

func (in *instance) running() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.state == StateRunning
}

func (in *instance) setUnavailable(reason string) {
	in.mu.Lock()
	changed := in.state != StateUnavailable || in.reason != reason
	in.state, in.reason = StateUnavailable, reason
	in.mu.Unlock()
	if changed {
		in.log.Printf("[%s] unavailable: %s", in.name, reason)
	}
}

// report logs repair actions and keeps a short tail for the status API.
func (in *instance) report(actions []string) {
	if len(actions) == 0 {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, a := range actions {
		in.log.Printf("[%s] %s", in.name, a)
		in.recent = append(in.recent, a)
	}
	if n := len(in.recent); n > 50 {
		in.recent = in.recent[n-50:]
	}
}
