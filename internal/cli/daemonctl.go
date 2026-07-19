package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/fsstore"
	"github.com/busovikov/takos/internal/registry"
)

// daemonCtl is the start/stop plumbing shared by the machine-global daemons
// (tracker :7434, runner :7433): flock probe, pid file carrying the API
// address, detached re-exec into the hidden `… run` foreground entry.
type daemonCtl struct {
	label       string // "tracker daemon"
	lockFile    string
	pidFile     string
	logFile     string
	defaultAddr string
	runArgv     []string // child argv of the hidden foreground entry
}

// running probes the machine-global flock: if we can take it, nobody holds
// it.
func (c daemonCtl) running() bool {
	lock, err := fsstore.TryLock(filepath.Join(registry.Home(), c.lockFile))
	if err != nil {
		return !os.IsNotExist(err)
	}
	lock.Unlock()
	return false
}

// readPid parses the pid file: line one is the pid, line two the API
// address.
func (c daemonCtl) readPid() (pid int, addr string, err error) {
	data, err := os.ReadFile(filepath.Join(registry.Home(), c.pidFile))
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
		addr = c.defaultAddr
	}
	return pid, addr, nil
}

// newStartCmd builds the `start` subcommand; run is the foreground body.
func (c daemonCtl) newStartCmd(run func(addr string) error) *cobra.Command {
	var foreground bool
	var addr string
	cmd := &cobra.Command{
		Use:   "start",
		Short: fmt.Sprintf("Start the %s (detached; --foreground to stay attached)", c.label),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if c.running() {
				return fmt.Errorf("%s is already running", c.label)
			}
			if foreground {
				return run(addr)
			}
			return c.startDetached(addr)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground")
	cmd.Flags().StringVar(&addr, "addr", c.defaultAddr, "API listen address")
	return cmd
}

// newRunCmd builds the hidden foreground entry the detached start re-execs.
func (c daemonCtl) newRunCmd(run func(addr string) error) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:    "run",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", c.defaultAddr, "API listen address")
	return cmd
}

func (c daemonCtl) startDetached(addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home := registry.Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(home, c.logFile)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	child := exec.Command(exe, append(append([]string{}, c.runArgv...), "--addr", addr)...)
	child.Stdout, child.Stderr = logFile, logFile
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return err
	}
	// The child holds the flock once it is up; give it a moment so an
	// immediate `status` tells the truth.
	for range 20 {
		if c.running() {
			ok("%s started (pid %d, log %s)", c.label, child.Process.Pid, logPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s did not come up — see %s", c.label, logPath)
}

func (c daemonCtl) newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: fmt.Sprintf("Stop the %s", c.label),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !c.running() {
				fmt.Printf("%s is not running\n", c.label)
				return nil
			}
			pid, _, err := c.readPid()
			if err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			for range 50 {
				if !c.running() {
					ok("%s stopped (pid %d)", c.label, pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("%s (pid %d) did not stop within 5s", c.label, pid)
		},
	}
}
