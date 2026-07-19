package trackerd

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
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
