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

	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/trackerd"
)

// newDaemonCmd builds the `takos daemon` group. The daemon is machine-global
// (one process supervising every registered workspace), so the group lives
// at the top level; `takos task daemon` stays as a hidden alias.
func newDaemonCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Tracker daemon: one process watching every registered workspace",
		Hidden: hidden,
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
			if daemonRunning() {
				return fmt.Errorf("tracker daemon is already running")
			}
			if foreground {
				return runDaemon(addr)
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			home := registry.Home()
			if err := os.MkdirAll(home, 0o755); err != nil {
				return err
			}
			logPath := filepath.Join(home, trackerd.GlobalLogFile)
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer logFile.Close()
			child := exec.Command(exe, "daemon", "run", "--addr", addr)
			child.Stdout, child.Stderr = logFile, logFile
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := child.Start(); err != nil {
				return err
			}
			// The child holds the flock once it is up; give it a moment so
			// an immediate `daemon status` tells the truth.
			for range 20 {
				if daemonRunning() {
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
			return runDaemon(addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", trackerd.DefaultAddr, "status API listen address")
	return cmd
}

func runDaemon(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return trackerd.Run(ctx, trackerd.Options{Addr: addr})
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the tracker daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !daemonRunning() {
				fmt.Println("tracker daemon is not running")
				return nil
			}
			pid, _, err := readPidFile()
			if err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			for range 50 {
				if !daemonRunning() {
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
		Short: "Tracker daemon status and its workspaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !daemonRunning() {
				if jsonOut {
					return json.NewEncoder(os.Stdout).Encode(map[string]any{"running": false})
				}
				fmt.Println("tracker daemon is not running")
				return nil
			}
			_, addr, err := readPidFile()
			if err != nil {
				return err
			}
			var st trackerd.DaemonStatus
			if err := fetchJSON(addr, "/api/status", &st); err != nil {
				return fmt.Errorf("daemon is running but its API did not answer at %s: %w", addr, err)
			}
			var wss []trackerd.WorkspaceSummary
			if err := fetchJSON(addr, "/api/workspaces", &wss); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"running": true, "addr": addr, "status": st, "workspaces": wss,
				})
			}
			fmt.Printf("tracker daemon: running · pid %d · %s\n", st.Pid, addr)
			fmt.Printf("up since %s · registry %s\n\n",
				st.StartedAt.Local().Format("02.01 15:04:05"), st.Registry)
			for _, ws := range wss {
				printWorkspaceSummary(ws)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

func printWorkspaceSummary(ws trackerd.WorkspaceSummary) {
	switch ws.State {
	case trackerd.StateRunning:
		total := 0
		for stage, n := range ws.Tasks {
			if stage != "backlog" {
				total += n
			}
		}
		fmt.Printf("  %-14s %s · %s · %d tasks (+%d backlog) · scan %s\n",
			ws.Name, ws.Root, orDash(ws.ActiveSprint), total, ws.Tasks["backlog"], ago(ws.LastScan))
	default:
		fmt.Printf("  %-14s %s · %s (%s)\n", ws.Name, ws.Root, ws.State, ws.Reason)
	}
}

// daemonRunning probes the machine-global flock: if we can take it, nobody
// holds it.
func daemonRunning() bool {
	lock, err := fsstore.TryLock(filepath.Join(registry.Home(), trackerd.GlobalLockFile))
	if err != nil {
		return true
	}
	lock.Unlock()
	return false
}

// readPidFile parses "trackerd.pid": line one is the pid, line two the API
// address.
func readPidFile() (pid int, addr string, err error) {
	data, err := os.ReadFile(filepath.Join(registry.Home(), trackerd.GlobalPidFile))
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

func fetchJSON(addr, path string, v any) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
