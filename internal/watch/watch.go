// Package watch delivers a debounced "the workspace changed" signal over
// fsnotify. Both consumers — the tracker daemon (to validate manual mv
// gestures) and the TUI (to redraw the live board) — only need a coalesced
// ping: what exactly changed is re-read from the filesystem anyway.
package watch

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher emits one struct{} on C after each burst of filesystem events.
type Watcher struct {
	C chan struct{}

	root string
	fsw  *fsnotify.Watcher
	done chan struct{}
}

// New watches the workspace's handler areas: root (workflow.yaml, new area
// dirs), backlog/, sprints/ and every sprint/stage folder. Directories
// created later (a new sprint) are added on the fly.
func New(root string, debounce time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		C:    make(chan struct{}, 1),
		root: root,
		fsw:  fsw,
		done: make(chan struct{}),
	}
	w.addTree(root, 0)
	go w.loop(debounce)
	return w, nil
}

// Close stops the watcher and closes C.
func (w *Watcher) Close() error {
	close(w.done)
	return w.fsw.Close()
}

// addTree registers dir and its subdirectories up to the stage-folder depth
// (root=0 → sprints=1 → sprint-12=2 → 1-refinement=3). Deeper levels are the
// tasks' own files reached through symlinks — not our business here.
func (w *Watcher) addTree(dir string, depth int) {
	w.fsw.Add(dir)
	if depth >= 3 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") ||
			(depth == 0 && !watchedArea(e.Name())) {
			continue
		}
		w.addTree(filepath.Join(dir, e.Name()), depth+1)
	}
}

func watchedArea(name string) bool {
	switch name {
	case "backlog", "sprints", "archive":
		return true
	}
	return false
}

func (w *Watcher) loop(debounce time.Duration) {
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				close(w.C)
				return
			}
			// A new directory (sprint promoted from archive, fresh next
			// sprint, stage folder) must be watched too.
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Lstat(ev.Name); err == nil && fi.IsDir() {
					if rel, err := filepath.Rel(w.root, ev.Name); err == nil {
						w.addTree(ev.Name, len(strings.Split(rel, string(os.PathSeparator))))
					}
				}
			}
			timer.Reset(debounce)
		case <-timer.C:
			select {
			case w.C <- struct{}{}:
			default: // a ping is already pending — coalesce
			}
		case <-w.fsw.Errors:
			// fsnotify errors are non-fatal here: the consumers also poll.
		case <-w.done:
			close(w.C)
			return
		}
	}
}
