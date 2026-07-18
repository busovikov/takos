package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
)

func newTaskMoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move T-0001 <stage|next|prev|backlog>",
		Short: "Переместить задачу (валидированный mv хендлера)",
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
				var ok bool
				if to == "next" {
					to, ok = ws.NextStage(pos.Stage)
				} else {
					to, ok = ws.PrevStage(pos.Stage)
				}
				if !ok {
					return fmt.Errorf("%s: из стадии %s некуда двигаться (%s)", id, pos.Stage, args[1])
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
	cmd.Flags().Bool("wait", false, "дождаться пайплайна автоматики (фаза B)")
	return cmd
}

func newTaskTakeCmd() *cobra.Command {
	var now bool
	cmd := &cobra.Command{
		Use:   "take T-0001",
		Short: "Взять задачу из беклога в следующий спринт (--now — в активный)",
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
	cmd.Flags().BoolVar(&now, "now", false, "в активный спринт вместо следующего")
	return cmd
}

func newTaskResolveCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "resolve T-0001",
		Short: "Отметить план готовым (check plan-resolved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(args[0], "plan-resolved", remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "снять отметку")
	return cmd
}

func newTaskCheckCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "check T-0001 <имя>",
		Short: "Поставить/снять файл-маркер .checks/<имя>",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(args[0], args[1], remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "снять маркер")
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
		ok("%s: check %s снят", id, name)
		return nil
	}
	if err := core.SetCheck(taskDir, name); err != nil {
		return err
	}
	core.AppendHistory(taskDir, "user", "check", name)
	ok("%s: check %s", id, name)
	return nil
}

func newTaskEditCmd() *cobra.Command {
	var plan bool
	cmd := &cobra.Command{
		Use:   "edit T-0001",
		Short: "Открыть task.md (или --plan → plan.md) в $EDITOR",
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
			editor := os.Getenv("EDITOR")
			if editor == "" {
				return fmt.Errorf("переменная $EDITOR не задана")
			}
			c := exec.Command(editor, filepath.Join(taskDir, file))
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().BoolVar(&plan, "plan", false, "открыть plan.md")
	return cmd
}

func newTaskSprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sprint",
		Short: "Статус спринта; sprint close — закрыть",
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
			fmt.Printf("СПРИНТ %s · активный\n", active.Name)
			fmt.Printf("начался %s · день %d из %d · закроется %s\n",
				active.Meta.StartedAt.Local().Format("02.01"), day, active.Meta.LengthDays,
				active.Meta.EndsAt.Local().Format("02.01"))
			if next != nil {
				fmt.Printf("следующий: %s\n", next.Name)
			}
			return nil
		},
	}
	var yes bool
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Закрыть активный спринт (done → архив, незакрытые → следующий)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("закрытие спринта необратимо переносит задачи — подтверди флагом --yes")
			}
			ws, err := openWS()
			if err != nil {
				return err
			}
			closed, promoted, carried, err := ws.CloseActiveSprint(time.Now())
			if err != nil {
				return err
			}
			ok("%s закрыт → archive/%s · перенесено задач: %d", closed, closed, carried)
			ok("%s теперь активный · создан следующий", promoted)
			return nil
		},
	}
	closeCmd.Flags().BoolVar(&yes, "yes", false, "подтверждение")
	cmd.AddCommand(closeCmd)
	return cmd
}

func newTaskWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Операции с workflow.yaml",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Проверить схему и кросс-ссылки workflow.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := workspaceFlag
			if root == "" {
				root = core.DefaultRoot()
			}
			data, err := os.ReadFile(filepath.Join(root, core.WorkflowFile))
			if err != nil {
				return err
			}
			if _, err := core.ParseWorkflow(data); err != nil {
				return err
			}
			ok("workflow.yaml валиден")
			return nil
		},
	})
	return cmd
}
