package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
			if bg {
				return fmt.Errorf("background sessions arrive with the runner (phase B) — run without --bg")
			}
			ws, err := openWS()
			if err != nil {
				return err
			}
			task, pos, plan, err := session.Plan(ws, args[0])
			if err != nil {
				return err
			}

			resume := false
			switch plan.Mode {
			case session.ModeResume:
				if forceNew {
					return fmt.Errorf("%s hit its limit — resuming is the only way in (drop --new)", plan.ResumeName)
				}
				resume = true
			case session.ModeAsk:
				switch {
				case forceNew:
				case term.IsTerminal(int(os.Stdin.Fd())):
					fmt.Printf("session %s was interrupted — continue it? [Y/n] ", plan.ResumeName)
					line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
					resume = !strings.HasPrefix(strings.TrimSpace(strings.ToLower(line)), "n")
				default:
					resume = true // non-interactive default: continue
				}
			}

			e, err := session.Build(ws, task, pos, plan, resume, role)
			if err != nil {
				return err
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
	cmd.Flags().BoolVar(&bg, "bg", false, "run in the background (phase B)")
	cmd.Flags().BoolVar(&forceNew, "new", false, "start a fresh session instead of continuing")
	cmd.Flags().StringVar(&role, "role", "", "role override (default: the stage's role)")
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
	cmd := &cobra.Command{
		Use:   "logs T-0001",
		Short: "Session log of a background role (phase B)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("background session logs arrive with the runner (phase B); interactive sessions talk to your terminal directly")
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "follow the log")
	return cmd
}
