package trackerd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
)

// startWebDaemon boots the daemon over a one-workspace registry and returns
// its API address plus the workspace and its first task.
func startWebDaemon(t *testing.T) (addr string, ws *core.Workspace, task *core.Task) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TAKOS_HOME", home)
	t.Setenv("TAKOS_WORKSPACE", "")

	var err error
	ws, err = core.Init(filepath.Join(home, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	task, _, err = ws.CreateTask("Web board task", core.CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}

	reg := &registry.Registry{}
	if _, err := reg.Add("alpha", ws.Root); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- Run(ctx, Options{Addr: "127.0.0.1:0"}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not shut down")
		}
	})

	addr = waitAddr(t, home)
	waitState(t, addr, map[string]string{"alpha": StateRunning})
	return addr, ws, task
}

func getJSON(t *testing.T, addr, path string, out any) {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, addr, path string, body any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post("http://"+addr+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWebBoardProjection(t *testing.T) {
	addr, ws, task := startWebDaemon(t)

	var board Board
	getJSON(t, addr, "/api/ws/alpha/board", &board)
	if board.Workspace != "alpha" {
		t.Errorf("workspace = %q", board.Workspace)
	}
	wantStages := []string{"refinement", "planning", "implementation", "review", "done"}
	if strings.Join(board.Stages, ",") != strings.Join(wantStages, ",") {
		t.Errorf("stages = %v, want %v", board.Stages, wantStages)
	}
	if len(board.Tasks) != 1 || board.Tasks[0].ID != task.Meta.ID || board.Tasks[0].Stage != "refinement" {
		t.Fatalf("tasks = %+v", board.Tasks)
	}

	// Move it and see the projection follow.
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	getJSON(t, addr, "/api/ws/alpha/board", &board)
	if board.Tasks[0].Stage != "planning" {
		t.Errorf("stage after move = %q, want planning", board.Tasks[0].Stage)
	}
}

func TestWebCardProjection(t *testing.T) {
	addr, ws, task := startWebDaemon(t)
	// Give the card something to show in each section.
	loaded, err := core.LoadTask(task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Body = core.SetSection(loaded.Body, "Objective", "ship the thing")
	loaded.Body = core.SetSection(loaded.Body, "Context", "it lives in pkg/foo")
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	_ = ws

	var card TaskCard
	getJSON(t, addr, "/api/ws/alpha/task/"+task.Meta.ID, &card)
	if card.ID != task.Meta.ID || card.Title != "Web board task" {
		t.Fatalf("card head = %+v", card)
	}
	if card.Objective != "ship the thing" || card.Context != "it lives in pkg/foo" {
		t.Errorf("sections = objective:%q context:%q", card.Objective, card.Context)
	}
	if card.Stage != "refinement" {
		t.Errorf("stage = %q", card.Stage)
	}
	if len(card.History) == 0 {
		t.Error("history tail is empty")
	}

	// An unknown task is a 404, not a 500.
	resp, err := http.Get("http://" + addr + "/api/ws/alpha/task/T-9999")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown task = %d, want 404", resp.StatusCode)
	}
}

func TestWebMoveMutation(t *testing.T) {
	addr, _, task := startWebDaemon(t)

	// A legal manual transition succeeds.
	resp := postJSON(t, addr, "/api/ws/alpha/task/"+task.Meta.ID+"/move",
		map[string]string{"to": "planning"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move = %d", resp.StatusCode)
	}

	// A move that skips the plan-resolved gate is refused with the
	// validator's phrase — 409, filesystem untouched.
	resp2 := postJSON(t, addr, "/api/ws/alpha/task/"+task.Meta.ID+"/move",
		map[string]string{"to": "implementation"})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("gated move = %d, want 409", resp2.StatusCode)
	}
}

func TestWebEventsSSE(t *testing.T) {
	addr, ws, task := startWebDaemon(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/ws/alpha/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	// The initial ping arrives on connect.
	if !readEvent(t, sc) {
		t.Fatal("no initial SSE event")
	}

	// A filesystem change triggers a broadcast the subscriber receives.
	done := make(chan bool, 1)
	go func() { done <- readEvent(t, sc) }()
	time.Sleep(100 * time.Millisecond)
	if _, err := ws.MoveTask(task.Meta.ID, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("SSE stream ended before the change event")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no SSE event after a filesystem change")
	}
}

// TestShutdownWithOpenSSEStream pins the fix for a hang found live: an SSE
// response never goes idle, so http.Server.Shutdown waited on it forever
// and a single open browser tab wedged `takos daemon stop`.
func TestShutdownWithOpenSSEStream(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TAKOS_HOME", home)
	t.Setenv("TAKOS_WORKSPACE", "")

	ws, err := core.Init(filepath.Join(home, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	reg := &registry.Registry{}
	if _, err := reg.Add("alpha", ws.Root); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- Run(ctx, Options{Addr: "127.0.0.1:0"}) }()
	addr := waitAddr(t, home)
	waitState(t, addr, map[string]string{"alpha": StateRunning})

	// Hold a stream open — and keep the body unread, as a live browser tab
	// effectively does between events.
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	req, _ := http.NewRequestWithContext(streamCtx, http.MethodGet, "http://"+addr+"/api/ws/alpha/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Shutdown must be prompt, not merely bounded: the stream is told to
	// drain (d.stopped), so the server never falls back on its timeout.
	started := time.Now()
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Errorf("shutdown took %v — the SSE stream did not drain on its own", elapsed)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("daemon hung on shutdown with an SSE stream open")
	}
}

// readEvent scans until it sees an SSE "event: changed" line (skipping
// keepalive comments), returning false at end of stream.
func readEvent(t *testing.T, sc *bufio.Scanner) bool {
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: changed") {
			return true
		}
	}
	return false
}
