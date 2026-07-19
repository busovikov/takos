package runnerd

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
)

// ErrNotRunning reports that no runner daemon holds the machine-global
// lock — callers fall back to the detached file mode.
var ErrNotRunning = errors.New("runner daemon is not running")

// Client talks to the runner daemon's API.
type Client struct {
	Addr string
	http *http.Client
}

// Connect probes the machine-global lock: a free lock means no daemon. The
// API address comes from the pid file (line two), defaulting to :7433.
func Connect() (*Client, error) {
	home := registry.Home()
	if _, err := os.Stat(home); err != nil {
		return nil, ErrNotRunning
	}
	if lock, err := fsstore.TryLock(filepath.Join(home, GlobalLockFile)); err == nil {
		lock.Unlock()
		return nil, ErrNotRunning
	}
	addr := DefaultAddr
	if data, err := os.ReadFile(filepath.Join(home, GlobalPidFile)); err == nil {
		lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
		if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
			addr = strings.TrimSpace(lines[1])
		}
	}
	return &Client{Addr: addr}, nil
}

// NewToken generates a submit idempotency token: retrying Submit with the
// same token never starts a second job.
func NewToken() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Submit starts (or, for a replayed clientToken, returns) a headless job.
func (c *Client) Submit(wsName, taskID string, req SubmitRequest) (JobInfo, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return JobInfo{}, err
	}
	var info JobInfo
	err = c.do(http.MethodPost,
		fmt.Sprintf("/api/ws/%s/tasks/%s/sessions", url.PathEscape(wsName), url.PathEscape(taskID)),
		bytes.NewReader(body), &info)
	return info, err
}

// Status fetches the daemon's status.
func (c *Client) Status() (DaemonStatus, error) {
	var st DaemonStatus
	err := c.do(http.MethodGet, "/api/status", nil, &st)
	return st, err
}

// Jobs lists the daemon's jobs in submission order.
func (c *Client) Jobs() ([]JobInfo, error) {
	var list []JobInfo
	err := c.do(http.MethodGet, "/api/jobs", nil, &list)
	return list, err
}

// Job fetches one job by id.
func (c *Client) Job(id string) (JobInfo, error) {
	var info JobInfo
	err := c.do(http.MethodGet, "/api/jobs/"+url.PathEscape(id), nil, &info)
	return info, err
}

// Cancel asks a running job to stop (SIGTERM to the agent).
func (c *Client) Cancel(id string) (JobInfo, error) {
	var info JobInfo
	err := c.do(http.MethodPost, "/api/jobs/"+url.PathEscape(id)+"/cancel", nil, &info)
	return info, err
}

func (c *Client) do(method, path string, body io.Reader, out any) error {
	if c.http == nil {
		c.http = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequest(method, "http://"+c.Addr+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return errors.New(strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
