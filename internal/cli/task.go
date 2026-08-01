package cli

import "github.com/spf13/cobra"

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Task tracker: board, create, move, review, sprints",
	}
	cmd.PersistentFlags().StringVar(&workspaceFlag, "workspace", "",
		"workspace name (registry) or path (default: $TAKOS_WORKSPACE → the workspace around the current directory)")
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
		newTaskAgentCmd(),
		newTaskAgentExecCmd(),
		newTaskCancelCmd(),
		newTaskReleaseCmd(),
		newTaskLogsCmd(),
		newTaskReviewCmd(),
		newTaskCommentCmd(),
		newTaskSprintCmd(),
		newTaskWorkflowCmd(),
		newTaskRoleCmd(),
		newDaemonCmd(true), // hidden alias of the top-level `takos daemon`
	)
	return cmd
}
