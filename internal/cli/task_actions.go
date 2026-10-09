package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/registry"
)

func newTaskMoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move T-0001 <stage|next|prev|backlog>",
		Short: "Move a task (a validated mv of its handler)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id, to := args[0], args[1]
			if to == "next" || to == "prev" {
				pos, err := ws.FindHandler(id)
				if err != nil {
					return err
				}
				var found bool
				if to == "next" {
					to, found = ws.NextStage(pos.Stage)
				} else {
					to, found = ws.PrevStage(pos.Stage)
				}
				if !found {
					return fmt.Errorf("%s: nowhere to go from stage %s (%s)", id, pos.Stage, args[1])
				}
			}
			res, err := ws.MoveTask(id, to, "user", "")
			if err != nil {
				return err
			}
			ok("%s: %s → %s", res.ID, res.From, res.To)
			return nil
		},
	}
	cmd.Flags().Bool("wait", false, "wait for the automation pipeline (phase B)")
	return cmd
}

func newTaskTakeCmd() *cobra.Command {
	var now bool
	cmd := &cobra.Command{
		Use:   "take T-0001",
		Short: "Pull a task from the backlog into the next sprint (--now: active)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			dest := "next"
			if now {
				dest = ""
			}
			first := ws.WF.Stages[0].ID
			res, err := ws.MoveTask(args[0], first, "user", dest)
			if err != nil {
				return err
			}
			pos, _ := ws.FindHandler(args[0])
			ok("%s: %s → %s (%s)", res.ID, res.From, res.To, pos.Sprint)
			return nil
		},
	}
	cmd.Flags().BoolVar(&now, "now", false, "into the active sprint instead of the next one")
	return cmd
}

func newTaskResolveCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "resolve T-0001",
		Short: "Mark the plan as ready (check plan-resolved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(args[0], "plan-resolved", remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "clear the mark")
	return cmd
}

func newTaskCheckCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "check T-0001 <name>",
		Short: "Set/clear the .checks/<name> marker file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(args[0], args[1], remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "clear the marker")
	return cmd
}

func runCheck(id, name string, remove bool) error {
	ws, err := openWS()
	if err != nil {
		return err
	}
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return err
	}
	if remove {
		if err := core.RemoveCheck(taskDir, name); err != nil {
			return err
		}
		core.AppendHistory(taskDir, "user", "uncheck", name)
		ok("%s: check %s cleared", id, name)
		return nil
	}
	if err := core.SetCheck(taskDir, name); err != nil {
		return err
	}
	core.AppendHistory(taskDir, "user", "check", name)
	ok("%s: check %s", id, name)
	return nil
}

func newTaskAutomateCmd() *cobra.Command {
	var off bool
	cmd := &cobra.Command{
		Use:   "automate T-0001",
		Short: "Turn a task's rule-driven automation on (default) or off",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAutomate(args[0], !off)
		},
	}
	cmd.Flags().BoolVar(&off, "off", false, "pause automation instead")
	return cmd
}

func runAutomate(id string, on bool) error {
	ws, err := openWS()
	if err != nil {
		return err
	}
	taskDir, err := ws.FindTaskDir(id)
	if err != nil {
		return err
	}
	if err := core.SetAutomation(taskDir, on); err != nil {
		return err
	}
	state := "active"
	if !on {
		state = "paused"
	}
	core.AppendHistory(taskDir, "user", "automation", state)
	ok("%s: automation %s", id, state)
	return nil
}

func newTaskEditCmd() *cobra.Command {
	var plan bool
	cmd := &cobra.Command{
		Use:   "edit T-0001",
		Short: "Open task.md (or --plan → plan.md) in $EDITOR, or the system default application",
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
			file := core.TaskFile
			if plan {
				file = core.PlanFile
			}
			path := filepath.Join(taskDir, file)
			if editor := os.Getenv("EDITOR"); editor != "" {
				c := exec.Command(editor, path)
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
				return c.Run()
			}
			// No override: the OS picks the application for the file.
			return core.OpenWithSystem(path).Start()
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "open plan.md")
	return cmd
}

func newTaskSprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sprint",
		Short: "Sprint status; sprint close — close it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			active, err := ws.ActiveSprint()
			if err != nil {
				return err
			}
			next, _ := ws.NextSprint()
			day := int(time.Since(active.Meta.StartedAt).Hours()/24) + 1
			fmt.Printf("SPRINT %s · active\n", active.Name)
			fmt.Printf("started %s · day %d of %d · closes %s\n",
				active.Meta.StartedAt.Local().Format("02.01"), day, active.Meta.LengthDays,
				active.Meta.EndsAt.Local().Format("02.01"))
			if next != nil {
				fmt.Printf("next: %s\n", next.Name)
			}
			return nil
		},
	}
	var yes bool
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Close the active sprint (done → archive, unfinished → next)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("closing a sprint irreversibly moves tasks — confirm with --yes")
			}
			ws, err := openWS()
			if err != nil {
				return err
			}
			closed, promoted, carried, err := ws.CloseActiveSprint(time.Now())
			if err != nil {
				return err
			}
			ok("%s closed → archive/%s · tasks carried over: %d", closed, closed, carried)
			ok("%s is now active · a new next sprint was created", promoted)
			return nil
		},
	}
	closeCmd.Flags().BoolVar(&yes, "yes", false, "confirm")
	cmd.AddCommand(closeCmd)
	return cmd
}

func newTaskWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "workflow.yaml operations",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate the schema and cross-references of workflow.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := registry.Resolve(workspaceFlag)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(filepath.Join(root, core.WorkflowFile))
			if err != nil {
				return err
			}
			if _, err := core.ParseWorkflow(data); err != nil {
				return err
			}
			ok("workflow.yaml is valid")
			return nil
		},
	})
	return cmd
}
