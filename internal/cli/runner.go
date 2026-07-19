package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/runnerd"
)

// runnerCtl drives the runner daemon's lifecycle files.
var runnerCtl = daemonCtl{
	label:       "runner daemon",
	lockFile:    runnerd.GlobalLockFile,
	pidFile:     runnerd.GlobalPidFile,
	logFile:     runnerd.GlobalLogFile,
	defaultAddr: runnerd.DefaultAddr,
	runArgv:     []string{"runner", "run"},
}

// newRunnerCmd builds the `takos runner` group — the machine-global daemon
// that executes headless agent sessions over the job API.
func newRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Runner daemon: executes headless agent sessions (:7433)",
	}
	cmd.AddCommand(
		runnerCtl.newStartCmd(runRunnerDaemon),
		runnerCtl.newStopCmd(),
		newRunnerStatusCmd(),
		runnerCtl.newRunCmd(runRunnerDaemon),
	)
	return cmd
}

func runRunnerDaemon(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runnerd.Run(ctx, runnerd.Options{Addr: addr})
}

func newRunnerStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Runner daemon status and its jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !runnerCtl.running() {
				if jsonOut {
					return json.NewEncoder(os.Stdout).Encode(map[string]any{"running": false})
				}
				fmt.Println("runner daemon is not running")
				return nil
			}
			_, addr, err := runnerCtl.readPid()
			if err != nil {
				return err
			}
			client := &runnerd.Client{Addr: addr}
			st, err := client.Status()
			if err != nil {
				return fmt.Errorf("daemon is running but its API did not answer at %s: %w", addr, err)
			}
			jobs, err := client.Jobs()
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"running": true, "addr": addr, "status": st, "jobs": jobs,
				})
			}
			fmt.Printf("runner daemon: running · pid %d · %s\n", st.Pid, addr)
			fmt.Printf("up since %s · %d running · %d done\n",
				st.StartedAt.Local().Format("02.01 15:04:05"),
				st.Jobs[runnerd.JobRunning], st.Jobs[runnerd.JobDone]+st.Jobs[runnerd.JobFailed])
			for _, j := range jobs {
				fmt.Printf("  %s\n", jobLine(j))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

// jobLine renders one job for the status table.
func jobLine(j runnerd.JobInfo) string {
	state := j.State
	when := ago(j.EndedAt)
	switch j.State {
	case runnerd.JobRunning:
		when = compactDur(time.Since(j.StartedAt))
	case runnerd.JobDone:
		state = j.Outcome
	case runnerd.JobFailed:
		state = "failed: " + j.Error
	}
	return fmt.Sprintf("%s  %s/%s · %s · %s · %s · %s",
		j.ID, j.Workspace, j.TaskID, j.Session, j.Role, state, when)
}

func compactDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
