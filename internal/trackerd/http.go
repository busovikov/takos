package trackerd

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// Status is the /api/status payload — enough for `takos task daemon status`
// and future clients (the web UI is another consumer of this API in phase C).
type Status struct {
	Pid       int       `json:"pid"`
	Workspace string    `json:"workspace"`
	StartedAt time.Time `json:"startedAt"`
	LastScan  time.Time `json:"lastScan"`
	Recent    []string  `json:"recent,omitempty"` // latest repair/validation actions
}

func (d *Daemon) serveHTTP(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		st := Status{
			Pid:       os.Getpid(),
			Workspace: d.ws.Root,
			StartedAt: d.startedAt,
			LastScan:  d.lastScan,
			Recent:    append([]string(nil), d.recent...),
		}
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			d.log.Printf("trackerd: http: %v", err)
		}
	}()
	return srv
}
