// Package trackerd is the tracker daemon: an fsnotify watcher over the
// workspace that validates manual mv gestures (rolling invalid ones back),
// repairs handler invariants via the reconciler, closes overdue sprints and
// serves a small status API on :7434. It is stateless — everything it knows
// is re-derived from the filesystem, so killing it never loses data.
package trackerd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/watch"
)

const (
	LockFile = ".trackerd.lock"
	PidFile  = ".trackerd.pid"
	LogFile  = ".trackerd.log"

	DefaultAddr = "127.0.0.1:7434"

	debounce = 500 * time.Millisecond
)

// Options tunes a daemon run.
type Options struct {
	Addr string // HTTP listen address; "" → DefaultAddr
}

// Daemon holds the run-time state of one tracker daemon.
type Daemon struct {
	ws   *core.Workspace
	log  *log.Logger
	snap map[string]core.HandlerPos // handler name → last known position

	mu        sync.Mutex
	startedAt time.Time
	lastScan  time.Time
	recent    []string // last repair/validation actions, newest last
}

// Run starts the daemon in the foreground and blocks until ctx is cancelled.
// A second daemon on the same workspace fails fast on the flock.
func Run(ctx context.Context, ws *core.Workspace, opts Options) error {
	lock, err := fsstore.TryLock(filepath.Join(ws.Root, LockFile))
	if err != nil {
		return fmt.Errorf("tracker daemon is already running for %s", ws.Root)
	}
	defer lock.Unlock()

	pidPath := filepath.Join(ws.Root, PidFile)
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if err := os.WriteFile(pidPath,
		[]byte(strconv.Itoa(os.Getpid())+"\n"+addr+"\n"), 0o644); err != nil {
		return err
	}
	defer os.Remove(pidPath)

	d := &Daemon{
		ws:        ws,
		log:       log.New(os.Stderr, "", log.LstdFlags),
		startedAt: time.Now(),
	}
	d.log.Printf("trackerd: workspace %s, pid %d", ws.Root, os.Getpid())

	// Initial reconciliation covers everything that happened while the
	// daemon was down (manual moves, overdue sprints, broken symlinks).
	rep, err := ws.Reconcile(time.Now())
	if err != nil {
		return err
	}
	d.report(rep.Actions)
	d.snap = d.snapshot()

	w, err := watch.New(ws.Root, debounce)
	if err != nil {
		return err
	}
	defer w.Close()

	srv := d.serveHTTP(addr)
	defer srv.Shutdown(context.Background())

	poll := time.Duration(ws.WF.Settings.PollIntervalSec) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Printf("trackerd: shutting down")
			return nil
		case _, ok := <-w.C:
			if !ok {
				return fmt.Errorf("trackerd: watcher closed unexpectedly")
			}
			d.scan(false)
		case <-ticker.C:
			// The poll tick is the fallback for missed fsnotify events and
			// the sprint scheduler's clock.
			d.scan(true)
		}
	}
}

// report logs repair actions and keeps a short tail for /api/status.
func (d *Daemon) report(actions []string) {
	if len(actions) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range actions {
		d.log.Printf("trackerd: %s", a)
		d.recent = append(d.recent, a)
	}
	if n := len(d.recent); n > 50 {
		d.recent = d.recent[n-50:]
	}
}
