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
		Short: "Review findings: list, or triage one (--resolve|--rework|--tech-debt)",
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
				fmt.Println("no review rounds")
				return nil
			}
			if len(args) == 1 {
				printRound(round, path)
				return nil
			}
			return triage(ws, taskDir, args[0], args[1], round, path, resolve, rework, techdebt)
		},
	}
	cmd.Flags().BoolVar(&resolve, "resolve", false, "the finding is settled")
	cmd.Flags().BoolVar(&rework, "rework", false, "needs rework")
	cmd.Flags().BoolVar(&techdebt, "tech-debt", false, "to tech debt (a backlog task)")
	return cmd
}

func printRound(round *core.ReviewRound, path string) {
	c := round.Counts()
	fmt.Printf("%s · %d findings · %d open · %d resolved · %d rework · %d tech-debt\n\n",
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
		return fmt.Errorf("pick exactly one of --resolve | --rework | --tech-debt")
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

	// Tech debt spawns a backlog task pointing back at its source.
	if st == core.ReviewTechDebt {
		item := findItem(round, itemID)
		task, err := loadTaskMeta(ws, id)
		if err == nil {
			debt, _, err := ws.CreateTask(item.Title, core.CreateOpts{
				Objective: fmt.Sprintf("%s\n\nsource: %s · %s#%s",
					item.Body, id, filepath.Base(path), itemID),
				Repo: task.Repo, Backlog: true,
			})
			if err != nil {
				return err
			}
			ok("tech-debt → %s %q created in the backlog", debt.Meta.ID, debt.Meta.Title)
		}
	}

	// Triage finished → verdict and auto-move.
	if v, done := round.Verdict(); done {
		c := round.Counts()
		fmt.Printf("round triaged: %d resolved · %d tech-debt · %d rework\n",
			c[core.ReviewResolved], c[core.ReviewTechDebt], c[core.ReviewRework])
		target := "done"
		if v == "rework" {
			target = "implementation"
			// The executor's next session opens on the rework findings.
			if err := core.AppendReworkToHandoff(taskDir, path, round); err == nil {
				ok("rework findings → hand-off.md (From review)")
			}
		}
		if res, err := ws.MoveTask(id, target, "engine", ""); err == nil {
			ok("%s → %s", res.ID, res.To)
		}
	} else {
		fmt.Printf("open left: %d\n", round.Counts()[core.ReviewOpen])
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
		Use:   "comment T-0001 \"TEXT\"",
		Short: "Add your own finding to the current review round",
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
			ok("finding %s added", itemID)
			return nil
		},
	}
}
