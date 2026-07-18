package cli

import "github.com/spf13/cobra"

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Трекер задач: доска, создание, перемещение, ревью, спринты",
	}
	cmd.PersistentFlags().StringVar(&workspaceFlag, "workspace", "",
		"путь к workspace (дефолт: $TAKOS_WORKSPACE или ~/.takos/workspace)")
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
