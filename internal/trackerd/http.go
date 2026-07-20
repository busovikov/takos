package trackerd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/webui"
)

// DaemonStatus is the /api/status payload.
type DaemonStatus struct {
	Pid        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
	Registry   string    `json:"registry"`
	Workspaces []string  `json:"workspaces"`
}

// WorkspaceSummary is one element of /api/workspaces — enough for the web
// UI's workspace switcher (phase C) and `takos workspace list`.
type WorkspaceSummary struct {
	Name         string         `json:"name"`
	Root         string         `json:"root"`
	State        string         `json:"state"`
	Reason       string         `json:"reason,omitempty"`
	ActiveSprint string         `json:"activeSprint,omitempty"`
	Tasks        map[string]int `json:"tasks,omitempty"` // stage (or "backlog") → count
	LastScan     time.Time      `json:"lastScan,omitempty"`
}

// InstanceStatus is the /api/ws/{name}/status payload.
type InstanceStatus struct {
	Name      string    `json:"name"`
	Root      string    `json:"root"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	LastScan  time.Time `json:"lastScan,omitempty"`
	Recent    []string  `json:"recent,omitempty"` // latest repair/validation actions
}

// serveHTTP starts the API listener; the actual address (Addr may carry
// port 0 in tests) lands in d.addr before the pid file is written.
func (d *Daemon) serveHTTP(addr string) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	d.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		st := DaemonStatus{
			Pid:       os.Getpid(),
			StartedAt: d.startedAt,
			Registry:  registry.Path(),
			Workspaces: func() []string {
				names := make([]string, 0, len(d.instances))
				for n := range d.instances {
					names = append(names, n)
				}
				sort.Strings(names)
				return names
			}(),
		}
		d.mu.Unlock()
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.workspaceSummaries())
	})

	mux.HandleFunc("GET /api/ws/{name}/status", func(w http.ResponseWriter, r *http.Request) {
		in := d.findInstance(r.PathValue("name"))
		if in == nil {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		in.mu.Lock()
		st := InstanceStatus{
			Name: in.name, Root: in.root, State: in.state, Reason: in.reason,
			StartedAt: in.startedAt, LastScan: in.lastScan,
			Recent: append([]string(nil), in.recent...),
		}
		in.mu.Unlock()
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/ws/{name}/board", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := d.openWorkspace(r.PathValue("name"))
		if !ok {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		board, err := buildBoard(r.PathValue("name"), ws)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, board)
	})

	mux.HandleFunc("GET /api/ws/{name}/task/{id}", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := d.openWorkspace(r.PathValue("name"))
		if !ok {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		card, err := buildCard(ws, r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, card)
	})

	mux.HandleFunc("POST /api/ws/{name}/task/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := d.openWorkspace(r.PathValue("name"))
		if !ok {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		var body struct {
			To     string `json:"to"`
			Sprint string `json:"sprint,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		// A human at the web UI drives a manual transition, exactly like Tab
		// in the TUI — same validator, same refusal phrase on rejection.
		res, err := ws.MoveTask(r.PathValue("id"), body.To, "user", body.Sprint)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("POST /api/ws/{name}/task/{id}/review/{item}", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := d.openWorkspace(r.PathValue("name"))
		if !ok {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		st := core.ReviewStatus(body.Status)
		switch st {
		case core.ReviewResolved, core.ReviewRework, core.ReviewTechDebt:
		default:
			http.Error(w, fmt.Sprintf("invalid status %q (resolved|rework|tech-debt)", body.Status), http.StatusBadRequest)
			return
		}
		res, err := ws.TriageFinding(r.PathValue("id"), r.PathValue("item"), st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("GET /api/ws/{name}/events", func(w http.ResponseWriter, r *http.Request) {
		in := d.findInstance(r.PathValue("name"))
		if in == nil {
			http.Error(w, "unknown workspace", http.StatusNotFound)
			return
		}
		d.streamEvents(w, r, in.hub)
	})

	// The embedded web UI takes the root; the /api patterns above are more
	// specific, so the ServeMux routes API calls to them first. Absent a
	// real build (placeholder only), the root simply isn't mounted.
	if ui, ok := webui.Handler(); ok {
		mux.Handle("GET /", ui)
	}

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.log.Printf("trackerd: http: %v", err)
		}
	}()
	return srv, nil
}

func (d *Daemon) findInstance(name string) *instance {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.instances[name]
}

// openWorkspace opens a fresh *core.Workspace for a request by registry
// name. A fresh handle per request (rather than the instance's loop-owned
// ws) keeps HTTP reads and mutations off the scan goroutine's object; the
// filesystem is the shared truth and every core op re-reads it. Only
// running instances are addressable.
func (d *Daemon) openWorkspace(name string) (*core.Workspace, bool) {
	in := d.findInstance(name)
	if in == nil || !in.running() {
		return nil, false
	}
	ws, err := core.Open(in.root)
	if err != nil {
		return nil, false
	}
	return ws, true
}

// streamEvents serves one SSE subscriber: an initial ping (so the client
// fetches immediately on connect), a "changed" event on every hub
// broadcast, and a keepalive comment so idle connections and proxies stay
// open. Returns when the client disconnects.
func (d *Daemon) streamEvents(w http.ResponseWriter, r *http.Request, hub *eventHub) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	fmt.Fprint(w, "event: changed\ndata: {}\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopped:
			return
		case <-ch:
			fmt.Fprint(w, "event: changed\ndata: {}\n\n")
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (d *Daemon) workspaceSummaries() []WorkspaceSummary {
	d.mu.Lock()
	list := make([]*instance, 0, len(d.instances))
	for _, in := range d.instances {
		list = append(list, in)
	}
	d.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	out := make([]WorkspaceSummary, 0, len(list))
	for _, in := range list {
		in.mu.Lock()
		s := WorkspaceSummary{
			Name: in.name, Root: in.root, State: in.state, Reason: in.reason,
			LastScan: in.lastScan,
		}
		in.mu.Unlock()
		// Counts are read straight off the filesystem — cheap globs, and
		// racing a concurrent scan only risks a transiently stale number.
		if in.running() {
			if ws, err := core.Open(in.root); err == nil {
				if active, err := ws.ActiveSprint(); err == nil {
					s.ActiveSprint = active.Name
					s.Tasks = map[string]int{}
					if handlers, err := ws.ListHandlers(""); err == nil {
						for _, h := range handlers {
							if filepath.Base(h.Path)[0] == '.' {
								continue
							}
							if h.Area == core.AreaBacklog {
								s.Tasks[core.Backlog]++
							} else if h.Sprint == active.Name {
								s.Tasks[h.Stage]++
							}
						}
					}
				}
			}
		}
		out = append(out, s)
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
