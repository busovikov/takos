package runnerd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
)

const (
	// Machine-global files under registry.Home() (~/.takos), mirroring the
	// tracker daemon's trio.
	GlobalLockFile = "runnerd.lock"
	GlobalPidFile  = "runnerd.pid"
	GlobalLogFile  = "runnerd.log"

	DefaultAddr = "127.0.0.1:7433"

	// keepJobs caps the in-memory job table: once past it the oldest
	// finished records (and their idempotency tokens) are dropped.
	keepJobs = 256
)

// Job states in JobInfo.State. A done job carries the session outcome
// (finished | limit | interrupted); failed means the job never ran to a
// classified ending (start failure, refused digest).
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Options tunes a daemon run.
type Options struct {
	Addr string // HTTP listen address; "" → DefaultAddr
}

// JobInfo is one headless run as the API reports it.
type JobInfo struct {
	ID          string    `json:"id"`
	ClientToken string    `json:"clientToken,omitempty"`
	Workspace   string    `json:"workspace"`
	TaskID      string    `json:"taskId"`
	Session     string    `json:"session"`
	UUID        string    `json:"uuid"`
	Role        string    `json:"role"`
	Stage       string    `json:"stage"`
	Resume      bool      `json:"resume"`
	State       string    `json:"state"`
	Outcome     string    `json:"outcome,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	EndedAt     time.Time `json:"endedAt,omitzero"`
	LogPath     string    `json:"logPath,omitempty"`
}

type jobRec struct {
	JobInfo
	cancel context.CancelFunc
}

// Daemon executes headless jobs submitted over the API. It is stateless
// across restarts by design: outcomes, transcripts and markers live in the
// task folders; the in-memory table only serves job queries and clientToken
// idempotency for the daemon's lifetime.
type Daemon struct {
	log       *log.Logger
	startedAt time.Time
	baseCtx   context.Context

	mu      sync.Mutex
	jobs    map[string]*jobRec // by job id
	order   []string           // job ids in submission order (list + prune)
	byToken map[string]string  // clientToken → job id
	active  map[string]string  // "<ws>/<task>" → running job id
	wg      sync.WaitGroup
	addr    string // actual HTTP address after listen
}

// Run starts the runner daemon in the foreground and blocks until ctx is
// cancelled; running jobs then get SIGTERM (via their contexts) and Run
// waits for their outcomes to be recorded. A second daemon on the same
// machine fails fast on the flock.
func Run(ctx context.Context, opts Options) error {
	home := registry.Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	lock, err := fsstore.TryLock(filepath.Join(home, GlobalLockFile))
	if err != nil {
		return fmt.Errorf("runner daemon is already running (%s)", home)
	}
	defer lock.Unlock()

	d := &Daemon{
		log:       log.New(os.Stderr, "", log.LstdFlags),
		startedAt: time.Now(),
		baseCtx:   ctx,
		jobs:      map[string]*jobRec{},
		byToken:   map[string]string{},
		active:    map[string]string{},
	}

	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	srv, err := d.serveHTTP(addr)
	if err != nil {
		return err
	}
	defer srv.Shutdown(context.Background())

	pidPath := filepath.Join(home, GlobalPidFile)
	if err := os.WriteFile(pidPath,
		[]byte(strconv.Itoa(os.Getpid())+"\n"+d.addr+"\n"), 0o644); err != nil {
		return err
	}
	defer os.Remove(pidPath)
	d.log.Printf("runnerd: pid %d, api %s", os.Getpid(), d.addr)

	<-ctx.Done()
	d.log.Printf("runnerd: shutting down")
	// Job contexts derive from ctx — every child got SIGTERM already; wait
	// for the executors to classify and record the endings.
	d.wg.Wait()
	return nil
}

// submit plans and starts one headless job. The returned status is
// http.StatusOK for an idempotent replay (same clientToken — the recorded
// job, no second start), http.StatusAccepted for a fresh start, or the
// refusal code with err set.
func (d *Daemon) submit(wsName, taskID string, req SubmitRequest) (JobInfo, int, error) {
	if info, ok := d.tokenReplay(req.ClientToken); ok {
		return info, http.StatusOK, nil
	}

	reg, err := registry.Load()
	if err != nil {
		return JobInfo{}, http.StatusInternalServerError, err
	}
	root := ""
	for _, e := range reg.Entries() {
		if e.Name == wsName {
			root = e.Root
			break
		}
	}
	if root == "" {
		return JobInfo{}, http.StatusNotFound, fmt.Errorf("unknown workspace %q", wsName)
	}
	ws, err := core.Open(root)
	if err != nil {
		return JobInfo{}, http.StatusConflict, err
	}
	job, err := PlanJob(ws, taskID, req.Role, req.New)
	if err != nil {
		return JobInfo{}, http.StatusConflict, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// Re-check under the lock: a concurrent retry with the same token, or a
	// parallel submit for the same task, may have won the race while we were
	// planning.
	if req.ClientToken != "" {
		if id, ok := d.byToken[req.ClientToken]; ok {
			return d.jobs[id].JobInfo, http.StatusOK, nil
		}
	}
	key := wsName + "/" + job.TaskID
	if id, ok := d.active[key]; ok {
		return d.jobs[id].JobInfo, http.StatusConflict,
			fmt.Errorf("session %s is already running", d.jobs[id].Session)
	}

	jobCtx, cancel := context.WithCancel(d.baseCtx)
	rec := &jobRec{
		JobInfo: JobInfo{
			ID:          newJobID(),
			ClientToken: req.ClientToken,
			Workspace:   wsName,
			TaskID:      job.TaskID,
			Session:     job.Name,
			UUID:        job.UUID,
			Role:        job.Role,
			Stage:       job.Stage,
			Resume:      job.Resume,
			State:       JobRunning,
			StartedAt:   time.Now(),
			LogPath:     job.LogPath,
		},
		cancel: cancel,
	}
	d.jobs[rec.ID] = rec
	d.order = append(d.order, rec.ID)
	if req.ClientToken != "" {
		d.byToken[req.ClientToken] = rec.ID
	}
	d.active[key] = rec.ID
	d.prune()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		outcome, err := job.Execute(jobCtx)
		d.mu.Lock()
		rec.EndedAt = time.Now()
		rec.Outcome = outcome
		rec.State = JobDone
		if err != nil {
			rec.State, rec.Error = JobFailed, err.Error()
		}
		delete(d.active, key)
		d.mu.Unlock()
		if err != nil {
			d.log.Printf("runnerd: job %s (%s): %v", rec.ID, rec.Session, err)
		} else {
			d.log.Printf("runnerd: job %s (%s) %s", rec.ID, rec.Session, outcome)
		}
	}()
	return rec.JobInfo, http.StatusAccepted, nil
}

// tokenReplay resolves a clientToken to its recorded job — the idempotency
// fast path for retried submissions.
func (d *Daemon) tokenReplay(token string) (JobInfo, bool) {
	if token == "" {
		return JobInfo{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if id, ok := d.byToken[token]; ok {
		return d.jobs[id].JobInfo, true
	}
	return JobInfo{}, false
}

// cancelJob asks a running job to stop: its context cancels, the executor
// sends SIGTERM and records the interrupted outcome.
func (d *Daemon) cancelJob(id string) (JobInfo, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.jobs[id]
	if !ok {
		return JobInfo{}, http.StatusNotFound, fmt.Errorf("unknown job %s", id)
	}
	if rec.State != JobRunning {
		return rec.JobInfo, http.StatusConflict, fmt.Errorf("job %s is already %s", id, rec.State)
	}
	rec.cancel()
	return rec.JobInfo, http.StatusAccepted, nil
}

// prune drops the oldest finished jobs past the table cap. Called under
// d.mu. A running job stops the sweep — it is never dropped, and the table
// cannot outgrow the cap by more than the number of live jobs.
func (d *Daemon) prune() {
	for len(d.order) > keepJobs {
		rec := d.jobs[d.order[0]]
		if rec.State == JobRunning {
			return
		}
		delete(d.jobs, rec.ID)
		if rec.ClientToken != "" {
			delete(d.byToken, rec.ClientToken)
		}
		d.order = d.order[1:]
	}
}

func newJobID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
