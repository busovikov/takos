package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/core"
)

func newTaskReviewCmd() *cobra.Command {
	var resolve, rework, techdebt bool
	cmd := &cobra.Command{
		Use:   "review T-0001 [R2]",
		Short: "Замечания ревью: список или триаж одного (--resolve|--rework|--tech-debt)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			round, path, err := core.LoadLatestRound(taskDir)
			if err != nil {
				return err
			}
			if round == nil {
				fmt.Println("раундов ревью нет")
				return nil
			}
			if len(args) == 1 {
				printRound(round, path)
				return nil
			}
			return triage(ws, taskDir, args[0], args[1], round, path, resolve, rework, techdebt)
		},
	}
	cmd.Flags().BoolVar(&resolve, "resolve", false, "проблема закрыта")
	cmd.Flags().BoolVar(&rework, "rework", false, "на доработку")
	cmd.Flags().BoolVar(&techdebt, "tech-debt", false, "в техдолг (задача в беклоге)")
	return cmd
}

func printRound(round *core.ReviewRound, path string) {
	c := round.Counts()
	fmt.Printf("%s · %d замечаний · %d open · %d resolved · %d rework · %d техдолг\n\n",
		filepath.Base(path), len(round.Items),
		c[core.ReviewOpen], c[core.ReviewResolved], c[core.ReviewRework], c[core.ReviewTechDebt])
	for _, it := range round.Items {
		fmt.Printf(" [%s] %s — %s\n", it.ID, it.Title, it.Status)
	}
}

func triage(ws *core.Workspace, taskDir, id, itemID string,
	round *core.ReviewRound, path string, resolve, rework, techdebt bool) error {

	var st core.ReviewStatus
	switch {
	case resolve && !rework && !techdebt:
		st = core.ReviewResolved
	case rework && !resolve && !techdebt:
		st = core.ReviewRework
	case techdebt && !resolve && !rework:
		st = core.ReviewTechDebt
	default:
		return fmt.Errorf("укажи ровно один из --resolve | --rework | --tech-debt")
	}
	if err := round.SetStatus(itemID, st); err != nil {
		return err
	}
	if err := core.SaveRound(path, round); err != nil {
		return err
	}
	core.AppendHistory(taskDir, "user", "triage",
		fmt.Sprintf("%s %s -> %s", filepath.Base(path), itemID, st))
	ok("%s → %s", itemID, st)

	// Техдолг — сразу задача в беклоге со ссылкой на источник.
	if st == core.ReviewTechDebt {
		item := findItem(round, itemID)
		task, err := loadTaskMeta(ws, id)
		if err == nil {
			debt, _, err := ws.CreateTask(item.Title, core.CreateOpts{
				Objective: fmt.Sprintf("%s\n\nисточник: %s · %s#%s",
					item.Body, id, filepath.Base(path), itemID),
				Repo: task.Repo, Backlog: true,
			})
			if err != nil {
				return err
			}
			ok("техдолг → %s «%s» создана в беклоге", debt.Meta.ID, debt.Meta.Title)
		}
	}

	// Триаж завершён → вердикт и авто-движение.
	if v, done := round.Verdict(); done {
		c := round.Counts()
		fmt.Printf("раунд обработан: %d resolved · %d техдолг · %d на доработку\n",
			c[core.ReviewResolved], c[core.ReviewTechDebt], c[core.ReviewRework])
		target := "done"
		if v == "rework" {
			target = "implementation"
		}
		if res, err := ws.MoveTask(id, target, "engine", ""); err == nil {
			ok("%s → %s", res.ID, res.To)
		}
	} else {
		fmt.Printf("осталось open: %d\n", round.Counts()[core.ReviewOpen])
	}
	return nil
}

func findItem(round *core.ReviewRound, itemID string) core.ReviewItem {
	for _, it := range round.Items {
		if it.ID == itemID {
			return it
		}
	}
	return core.ReviewItem{}
}

func loadTaskMeta(ws *core.Workspace, id string) (*core.TaskMeta, error) {
	dir, err := ws.FindTaskDir(id)
	if err != nil {
		return nil, err
	}
	task, err := core.LoadTask(dir)
	if err != nil {
		return nil, err
	}
	return &task.Meta, nil
}

func newTaskCommentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment T-0001 \"ТЕКСТ\"",
		Short: "Добавить своё замечание в текущий раунд ревью",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			taskDir, err := ws.FindTaskDir(args[0])
			if err != nil {
				return err
			}
			itemID, err := core.AddComment(taskDir, args[0], args[1])
			if err != nil {
				return err
			}
			ok("замечание %s добавлено", itemID)
			return nil
		},
	}
}
