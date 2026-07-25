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

	res, err := ws.TriageFinding(id, itemID, st)
	if err != nil {
		return err
	}
	ok("%s → %s", res.ItemID, res.Status)
	if res.DebtID != "" {
		ok("tech-debt → %s created in the backlog", res.DebtID)
	}
	if res.Done {
		if res.Verdict == "rework" {
			ok("rework findings → hand-off.md (From review)")
		}
		if res.MovedTo != "" {
			ok("round triaged · %s → %s", id, res.MovedTo)
		} else if res.Verdict == "done" {
			ok("round triaged · all findings settled — takos task move %s done when ready", id)
		}
	} else {
		round, _, _ := core.LoadLatestRound(taskDir)
		if round != nil {
			fmt.Printf("open left: %d\n", round.Counts()[core.ReviewOpen])
		}
	}
	return nil
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
