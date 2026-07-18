package cli

import "github.com/spf13/cobra"

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Task tracker: board, create, move, review, sprints",
	}
	cmd.PersistentFlags().StringVar(&workspaceFlag, "workspace", "",
		"workspace path (default: $TAKOS_WORKSPACE or ~/.takos/workspace)")
	cmd.AddCommand(
		newTaskInitCmd(),
		newTaskCreateCmd(),
		newTaskListCmd(),
		newTaskShowCmd(),
		newTaskHistoryCmd(),
		newTaskMoveCmd(),
		newTaskTakeCmd(),
		newTaskResolveCmd(),
		newTaskCheckCmd(),
		newTaskEditCmd(),
		newTaskReviewCmd(),
		newTaskCommentCmd(),
		newTaskSprintCmd(),
		newTaskWorkflowCmd(),
		newTaskRoleCmd(),
	)
	return cmd
}
