package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/trackerd"
)

func newTaskDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Tracker daemon: watcher, mv validation, reconciler, sprint scheduler",
	}
	cmd.AddCommand(newDaemonStartCmd(), newDaemonStopCmd(), newDaemonStatusCmd(), newDaemonRunCmd())
	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool
	var addr string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the tracker daemon (detached; --foreground to stay attached)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWSRaw()
			if err != nil {
				return err
			}
			if daemonRunning(ws.Root) {
				return fmt.Errorf("tracker daemon is already running for %s", ws.Root)
			}
			if foreground {
				return runDaemon(ws, addr)
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			logPath := filepath.Join(ws.Root, trackerd.LogFile)
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer logFile.Close()
			child := exec.Command(exe, "task", "daemon", "run",
				"--workspace", ws.Root, "--addr", addr)
			child.Stdout, child.Stderr = logFile, logFile
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := child.Start(); err != nil {
				return err
			}
			// The child holds the flock once it is up; give it a moment so
			// an immediate `daemon status` tells the truth.
			for range 20 {
				if daemonRunning(ws.Root) {
					ok("tracker daemon started (pid %d, log %s)", child.Process.Pid, logPath)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not come up — see %s", logPath)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground")
	cmd.Flags().StringVar(&addr, "addr", trackerd.DefaultAddr, "status API listen address")
	return cmd
}

// newDaemonRunCmd is the hidden foreground entry the detached start re-execs.
func newDaemonRunCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:    "run",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWSRaw()
			if err != nil {
				return err
			}
			return runDaemon(ws, addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", trackerd.DefaultAddr, "status API listen address")
	return cmd
}

func runDaemon(ws *core.Workspace, addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return trackerd.Run(ctx, ws, trackerd.Options{Addr: addr})
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the tracker daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWSRaw()
			if err != nil {
				return err
			}
			if !daemonRunning(ws.Root) {
				fmt.Println("tracker daemon is not running")
				return nil
			}
			pid, _, err := readPidFile(ws.Root)
			if err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			for range 50 {
				if !daemonRunning(ws.Root) {
					ok("tracker daemon stopped (pid %d)", pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon (pid %d) did not stop within 5s", pid)
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Tracker daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWSRaw()
			if err != nil {
				return err
			}
			if !daemonRunning(ws.Root) {
				if jsonOut {
					return json.NewEncoder(os.Stdout).Encode(map[string]any{"running": false})
				}
				fmt.Println("tracker daemon is not running")
				return nil
			}
			_, addr, err := readPidFile(ws.Root)
			if err != nil {
				return err
			}
			st, err := fetchStatus(addr)
			if err != nil {
				return fmt.Errorf("daemon is running but its API did not answer at %s: %w", addr, err)
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"running": true, "addr": addr, "status": st,
				})
			}
			fmt.Printf("tracker daemon: running · pid %d · %s\n", st.Pid, addr)
			fmt.Printf("workspace: %s\n", st.Workspace)
			fmt.Printf("up since %s · last scan %s\n",
				st.StartedAt.Local().Format("02.01 15:04:05"), ago(st.LastScan))
			for _, a := range tail(st.Recent, 5) {
				fmt.Printf("  · %s\n", a)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

// daemonRunning probes the daemon's flock: if we can take it, nobody holds it.
func daemonRunning(root string) bool {
	lock, err := fsstore.TryLock(filepath.Join(root, trackerd.LockFile))
	if err != nil {
		return true
	}
	lock.Unlock()
	return false
}

// readPidFile parses ".trackerd.pid": line one is the pid, line two the API
// address.
func readPidFile(root string) (pid int, addr string, err error) {
	data, err := os.ReadFile(filepath.Join(root, trackerd.PidFile))
	if err != nil {
		return 0, "", fmt.Errorf("daemon pid file: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	pid, err = strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0, "", fmt.Errorf("daemon pid file: %w", err)
	}
	if len(lines) > 1 {
		addr = strings.TrimSpace(lines[1])
	}
	if addr == "" {
		addr = trackerd.DefaultAddr
	}
	return pid, addr, nil
}

func fetchStatus(addr string) (*trackerd.Status, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var st trackerd.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
