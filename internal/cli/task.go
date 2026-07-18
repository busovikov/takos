package cli

import "github.com/spf13/cobra"

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Трекер задач: доска, создание, перемещение, ревью, спринты",
	}
	// Субкоманды (init, list, create, move, …) появляются в фазе A3.
	return cmd
}
