package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
	"github.com/busovikov/takos/internal/runnerd"
	"github.com/busovikov/takos/internal/session"
)

func newTaskAgentCmd() *cobra.Command {
	var bg, forceNew bool
	var role string
	cmd := &cobra.Command{
		Use:   "agent T-0001",
		Short: "Run the stage's agent session interactively (--session-id/--resume)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			if bg {
				return startBackground(ws, args[0], role, forceNew)
			}
			task, pos, plan, err := session.Plan(ws, args[0])
			if err != nil {
				return err
			}

			resume := false
			switch {
			case plan.Mode == session.ModeResume:
				if forceNew {
					return fmt.Errorf("%s hit its limit — resuming is the only way in (drop --new)", plan.ResumeName)
				}
				resume = true
			case plan.ResumeName != "":
				// A prior session in this stage can be continued (interrupted,
				// or a clean finish) — same choice the card offers.
				switch {
				case forceNew:
				case term.IsTerminal(int(os.Stdin.Fd())):
					fmt.Printf("continue session %s? [Y/n] ", plan.ResumeName)
					line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
					resume = !strings.HasPrefix(strings.TrimSpace(strings.ToLower(line)), "n")
				default:
					// No one at the keyboard: continue an interrupted session
					// (don't lose work), but start fresh after a clean finish.
					resume = plan.Mode == session.ModeAsk
				}
			}

			e, err := session.Build(ws, task, pos, plan, resume, role)
			if err != nil {
				return err
			}
			// A fresh entry regenerates the stage's hand-off digest first
			// (headless summarizer), so the session opens on condensed facts.
			if !resume {
				pre, err := runnerd.HandoffJob(ws, task, pos.Stage, plan.Reason)
				if err != nil {
					return err
				}
				if pre != nil {
					fmt.Printf("↻ %s · %s · regenerating the hand-off digest\n", pre.Name, pre.Role)
					outcome, err := pre.Execute(cmd.Context())
					if err != nil {
						return err
					}
					if outcome != session.OutcomeFinished {
						return fmt.Errorf("hand-off digest %s %s — session %s was not started",
							pre.Name, outcome, e.Name)
					}
				}
			}
			mode := "new"
			if e.Resume {
				mode = "resume"
			}
			fmt.Printf("▶ %s · %s · %s (%s)\n", e.Name, e.Role, e.Argv[0], mode)
			outcome, err := e.Run()
			if err != nil {
				return err
			}
			if outcome == session.OutcomeFinished {
				ok("session %s finished", e.Name)
				return nil
			}
			return fmt.Errorf("session %s %s", e.Name, outcome)
		},
	}
	cmd.Flags().BoolVar(&bg, "bg", false, "run headless in the background (transcript → task logs)")
	cmd.Flags().BoolVar(&forceNew, "new", false, "start a fresh session instead of continuing")
	cmd.Flags().StringVar(&role, "role", "", "role override (default: the stage's role)")
	return cmd
}

// startBackground hands the job to the runner daemon when one is up (its
// API owns headless runs: job table, cancel, idempotent retries). Without a
// daemon — or for a workspace the registry cannot name — it falls back to
// the file mode: validate in-process, detach a child takos to run it.
func startBackground(ws *core.Workspace, id, role string, forceNew bool) error {
	if name, registered := registry.NameFor(ws.Root); registered {
		client, err := runnerd.Connect()
		if err == nil {
			info, err := client.Submit(name, id, runnerd.SubmitRequest{
				Role: role, New: forceNew, ClientToken: runnerd.NewToken(),
			})
			if err != nil {
				return err
			}
			ok("headless session %s started (%s, runner daemon, job %s) — takos task logs %s -f",
				info.Session, info.Role, info.ID, id)
			return nil
		}
		if !errors.Is(err, runnerd.ErrNotRunning) {
			return err
		}
	}
	job, err := runnerd.PlanJob(ws, id, runnerd.JobOpts{Role: role, ForceNew: forceNew})
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"task", "agent-exec", id, "--workspace", ws.Root}
	if role != "" {
		args = append(args, "--role", role)
	}
	if forceNew {
		args = append(args, "--new")
	}
	child := exec.Command(exe, args...)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return err
	}
	ok("headless session %s started (%s) — takos task logs %s -f", job.Name, job.Role, id)
	return nil
}

// newTaskAgentExecCmd is the hidden foreground entry the detached --bg
// start re-execs — the file fallback when no runner daemon is up.
func newTaskAgentExecCmd() *cobra.Command {
	var forceNew bool
	var role string
	cmd := &cobra.Command{
		Use:    "agent-exec T-0001",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWSRaw()
			if err != nil {
				return err
			}
			job, err := runnerd.PlanJob(ws, args[0], runnerd.JobOpts{Role: role, ForceNew: forceNew})
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			outcome, err := job.Execute(ctx)
			if err != nil {
				return err
			}
			if outcome != session.OutcomeFinished {
				return fmt.Errorf("session %s %s", job.Name, outcome)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&forceNew, "new", false, "")
	cmd.Flags().StringVar(&role, "role", "", "")
	return cmd
}

func newTaskCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel T-0001",
		Short: "Interrupt the task's running session (SIGTERM)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			m, err := session.Cancel(taskDir)
			if err != nil {
				return err
			}
			ok("SIGTERM → session %s (pid %d)", m.Name, m.PID)
			return nil
		},
	}
}

func newTaskLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs T-0001",
		Short: "Transcript of the task's latest headless session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			ms := session.Markers(taskDir)
			// With -f right after `agent --bg` the marker may not exist
			// yet — give the detached child a moment.
			for wait := 0; follow && len(ms) == 0 && wait < 30; wait++ {
				time.Sleep(100 * time.Millisecond)
				ms = session.Markers(taskDir)
			}
			if len(ms) == 0 {
				return fmt.Errorf("%s has no sessions yet", args[0])
			}
			name := ms[len(ms)-1].Name
			path := filepath.Join(taskDir, session.SessionsDirName, name+".log")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("session %s left no transcript (interactive sessions talk to your terminal directly)", name)
			}
			return catLog(path, taskDir, follow)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow while the session runs")
	return cmd
}

// catLog prints a transcript; with follow it keeps reading appended bytes
// until the session is no longer running.
func catLog(path, taskDir string, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return err
	}
	for follow {
		running := session.Active(taskDir) != nil
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return err
		}
		if !running {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}
