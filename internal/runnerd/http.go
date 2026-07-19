package runnerd

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/busovikov/takos/internal/registry"
)

// SubmitRequest is the POST /api/ws/{name}/tasks/{task}/sessions body.
type SubmitRequest struct {
	Role        string `json:"role,omitempty"`        // role override (default: the stage's role)
	New         bool   `json:"new,omitempty"`         // force a fresh generation
	PromptExtra string `json:"promptExtra,omitempty"` // fills the {{promptExtra}} prompt token
	// ClientToken makes the submit idempotent: a retry with the same token
	// returns the recorded job instead of starting a second one.
	ClientToken string `json:"clientToken,omitempty"`
}

// DaemonStatus is the /api/status payload.
type DaemonStatus struct {
	Pid       int            `json:"pid"`
	StartedAt time.Time      `json:"startedAt"`
	Registry  string         `json:"registry"`
	Jobs      map[string]int `json:"jobs"` // state → count
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
		st := DaemonStatus{
			Pid:       os.Getpid(),
			StartedAt: d.startedAt,
			Registry:  registry.Path(),
			Jobs:      map[string]int{},
		}
		d.mu.Lock()
		for _, rec := range d.jobs {
			st.Jobs[rec.State]++
		}
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		list := make([]JobInfo, 0, len(d.order))
		for _, id := range d.order {
			list = append(list, d.jobs[id].JobInfo)
		}
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, list)
	})

	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		rec, ok := d.jobs[r.PathValue("id")]
		var info JobInfo
		if ok {
			info = rec.JobInfo
		}
		d.mu.Unlock()
		if !ok {
			http.Error(w, "unknown job "+r.PathValue("id"), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, info)
	})

	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		info, status, err := d.cancelJob(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, status, info)
	})

	mux.HandleFunc("POST /api/ws/{name}/tasks/{task}/sessions", func(w http.ResponseWriter, r *http.Request) {
		var req SubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		info, status, err := d.submit(r.PathValue("name"), r.PathValue("task"), req)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, status, info)
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.log.Printf("runnerd: http: %v", err)
		}
	}()
	return srv, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
