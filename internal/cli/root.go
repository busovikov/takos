// Package cli wires the takos command tree (cobra).
package cli

import (
	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/version"
)

// Execute runs the root command; it is the single entry point for cmd/takos.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "takos",
		Short:         "Takos — ФС-нативный трекер задач и оркестратор coding-агентов",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newTaskCmd())
	return root
}
