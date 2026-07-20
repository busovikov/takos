// Package trackerd is the tracker daemon: one process supervising every
// registered workspace. Each workspace gets an independent instance
// (fsnotify watcher + mv validation/rollback + reconciler + sprint
// scheduler); the supervisor owns the machine-global lock, the registry
// hot-reload and the HTTP API on :7434. The daemon is stateless — all it
// knows is re-derived from the filesystem, so killing it never loses data.
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

	"github.com/fsnotify/fsnotify"

	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
)

const (
	// Machine-global files under registry.Home() (~/.takos).
	GlobalLockFile = "trackerd.lock"
	GlobalPidFile  = "trackerd.pid"
	GlobalLogFile  = "trackerd.log"

	// Per-workspace flock inside the workspace root. Held by the running
	// instance; the CLI's sprint catch-up fallback probes exactly this.
	WorkspaceLockFile = ".trackerd.lock"

	DefaultAddr = "127.0.0.1:7434"

	debounce      = 500 * time.Millisecond
	retryInterval = 10 * time.Second
)

// Options tunes a daemon run.
type Options struct {
	Addr string // HTTP listen address; "" → DefaultAddr
}

// Daemon supervises the per-workspace instances.
type Daemon struct {
	log       *log.Logger
	startedAt time.Time

	// stopped is closed when shutdown begins. SSE streams select on it:
	// a long-lived response never goes idle, so without this signal
	// http.Server.Shutdown would wait on it forever.
	stopped chan struct{}

	mu        sync.Mutex
	instances map[string]*instance // by registry name
	addr      string               // actual HTTP address after listen
}

// Run starts the daemon in the foreground and blocks until ctx is
// cancelled. A second daemon on the same machine fails fast on the flock.
func Run(ctx context.Context, opts Options) error {
	home := registry.Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	lock, err := fsstore.TryLock(filepath.Join(home, GlobalLockFile))
	if err != nil {
		return fmt.Errorf("tracker daemon is already running (%s)", home)
	}
	defer lock.Unlock()

	d := &Daemon{
		log:       log.New(os.Stderr, "", log.LstdFlags),
		startedAt: time.Now(),
		stopped:   make(chan struct{}),
		instances: map[string]*instance{},
	}

	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	srv, err := d.serveHTTP(addr)
	if err != nil {
		return err
	}
	// Bounded shutdown: streams get a moment to notice d.stopped and drain,
	// then the listener is closed outright rather than hanging the process.
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			srv.Close()
		}
	}()

	pidPath := filepath.Join(home, GlobalPidFile)
	if err := os.WriteFile(pidPath,
		[]byte(strconv.Itoa(os.Getpid())+"\n"+d.addr+"\n"), 0o644); err != nil {
		return err
	}
	defer os.Remove(pidPath)
	d.log.Printf("trackerd: pid %d, api %s, registry %s", os.Getpid(), d.addr, registry.Path())

	d.reload(ctx)

	// Hot-reload: `takos workspace add|remove|use` edits workspaces.yaml —
	// picked up without a restart. The whole home dir is watched (atomic
	// writes replace the file); other files there (our own log!) are
	// filtered out.
	regEvents, closeWatch := watchRegistry(home, d.log)
	defer closeWatch()

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Printf("trackerd: shutting down")
			close(d.stopped) // SSE streams return, so Shutdown can finish
			d.stopAll()
			return nil
		case <-regEvents:
			d.reload(ctx)
		case <-ticker.C:
			// Retry unavailable instances (workspace appeared, lock freed).
			d.mu.Lock()
			list := make([]*instance, 0, len(d.instances))
			for _, in := range d.instances {
				list = append(list, in)
			}
			d.mu.Unlock()
			for _, in := range list {
				if !in.running() {
					in.tryStart(ctx)
				}
			}
		}
	}
}

// reload diffs the registry against the running instances: new entries
// start, removed ones stop, changed roots restart.
func (d *Daemon) reload(ctx context.Context) {
	reg, err := registry.Load()
	if err != nil {
		d.log.Printf("trackerd: registry: %v", err)
		return
	}
	desired := reg.Entries()

	// The map is mutated under the lock (HTTP reads it); the potentially
	// slow starts (initial reconcile) happen outside.
	var toStart []*instance
	d.mu.Lock()
	seen := map[string]bool{}
	for _, e := range desired {
		seen[e.Name] = true
		if in, ok := d.instances[e.Name]; ok {
			if in.root == e.Root {
				continue
			}
			d.log.Printf("trackerd: workspace %s moved %s → %s", e.Name, in.root, e.Root)
			in.stop()
		}
		in := newInstance(e.Name, e.Root, d.log)
		d.instances[e.Name] = in
		toStart = append(toStart, in)
	}
	for name, in := range d.instances {
		if !seen[name] {
			d.log.Printf("trackerd: workspace %s unregistered", name)
			in.stop()
			delete(d.instances, name)
		}
	}
	d.mu.Unlock()

	for _, in := range toStart {
		in.tryStart(ctx)
	}
}

func (d *Daemon) stopAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, in := range d.instances {
		in.stop()
	}
}

// watchRegistry emits a debounced ping when workspaces.yaml changes.
func watchRegistry(home string, lg *log.Logger) (<-chan struct{}, func()) {
	out := make(chan struct{}, 1)
	fsw, err := fsnotify.NewWatcher()
	if err != nil || fsw.Add(home) != nil {
		lg.Printf("trackerd: registry watch unavailable: %v", err)
		return out, func() {}
	}
	done := make(chan struct{})
	go func() {
		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
		for {
			select {
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) == registry.FileName {
					timer.Reset(debounce)
				}
			case <-timer.C:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-fsw.Errors:
			case <-done:
				return
			}
		}
	}()
	return out, func() { close(done); fsw.Close() }
}
