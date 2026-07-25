package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/trackerd"
)

// trackerCtl drives the tracker daemon's lifecycle files.
var trackerCtl = daemonCtl{
	label:       "tracker daemon",
	lockFile:    trackerd.GlobalLockFile,
	pidFile:     trackerd.GlobalPidFile,
	logFile:     trackerd.GlobalLogFile,
	defaultAddr: trackerd.DefaultAddr,
	runArgv:     []string{"daemon", "run"},
}

// newDaemonCmd builds the `takos daemon` group. The daemon is machine-global
// (one process supervising every registered workspace), so the group lives
// at the top level; `takos task daemon` stays as a hidden alias.
func newDaemonCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Tracker daemon: one process watching every registered workspace",
		Hidden: hidden,
	}
	cmd.AddCommand(
		trackerCtl.newStartCmd(runTrackerDaemon),
		trackerCtl.newStopCmd(),
		newDaemonStatusCmd(),
		trackerCtl.newRunCmd(runTrackerDaemon),
	)
	return cmd
}

func runTrackerDaemon(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return trackerd.Run(ctx, trackerd.Options{Addr: addr})
}

func newDaemonStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Tracker daemon status and its workspaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !trackerCtl.running() {
				if jsonOut {
					return json.NewEncoder(os.Stdout).Encode(map[string]any{"running": false})
				}
				fmt.Println("tracker daemon is not running")
				return nil
			}
			_, addr, err := trackerCtl.readPid()
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

func fetchJSON(addr, path string, v any) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
