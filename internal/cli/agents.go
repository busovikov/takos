package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/busovikov/takos/internal/agents"
	"github.com/busovikov/takos/internal/core"
)

func newAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "Detected agent CLIs: versions and models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf(" %-13s %-22s %-10s %s\n", "AGENT", "VERSION", "STATUS", "MODELS")
			for _, a := range agents.Detect() {
				status, extra := "✔ found", strings.Join(a.Models, " · ")
				if !a.Available {
					status, extra = "✗ missing", "install: "+a.Install
				}
				fmt.Printf(" %-13s %-22s %-10s %s\n", a.ID, orDash(a.Version), status, extra)
			}
			return nil
		},
	}
}

func newTaskRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Agent-to-role assignments of the workflow",
	}
	cmd.AddCommand(newRoleListCmd(), newRoleSetCmd())
	return cmd
}

func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Roles and their assigned agents/models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			detected := map[string]bool{}
			for _, a := range agents.Detect() {
				detected[a.ID] = a.Available
			}
			fmt.Printf(" %-12s %-13s %-26s %s\n", "ROLE", "AGENT", "MODEL", "")
			for _, name := range sortedRoleNames(ws.WF.Roles) {
				r := ws.WF.Roles[name]
				note := ""
				if !detected[r.Agent] {
					note = "⚠ agent not found on this system"
				}
				fmt.Printf(" %-12s %-13s %-26s %s\n", name, r.Agent, orDash(r.Model), note)
			}
			return nil
		},
	}
}

func sortedRoleNames(roles map[string]core.Role) []string {
	order := []string{"context", "planner", "executor", "handover", "reviewer", "summarizer"}
	var out []string
	seen := map[string]bool{}
	for _, n := range order {
		if _, ok := roles[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	for n := range roles {
		if !seen[n] {
			out = append(out, n)
		}
	}
	return out
}

func newRoleSetCmd() *cobra.Command {
	var agent, model string
	var force bool
	cmd := &cobra.Command{
		Use:   "set <role>",
		Short: "Assign an agent/model to a role (writes to workflow.yaml)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			role := args[0]
			if _, exists := ws.WF.Roles[role]; !exists {
				return fmt.Errorf("role %q is not declared in workflow.yaml", role)
			}
			if agent == "" {
				return fmt.Errorf("specify --agent (see takos agents)")
			}
			if !force {
				info, known := agents.ByID(agent)
				if !known {
					return fmt.Errorf("agent %q is not supported (takos agents)", agent)
				}
				if !info.Available {
					return fmt.Errorf("agent %q not found on this system (takos agents; --force to write it for another machine)", agent)
				}
			}
			if err := core.SetRoleAssignment(ws.Root, role, agent, model); err != nil {
				return err
			}
			if model != "" {
				ok("%s → %s · %s (written to workflow.yaml)", role, agent, model)
			} else {
				ok("%s → %s (written to workflow.yaml)", role, agent)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "agent id from takos agents")
	cmd.Flags().StringVar(&model, "model", "", "model (e.g. ollama/qwen3-coder:30b)")
	cmd.Flags().BoolVar(&force, "force", false, "write even when the agent is missing")
	return cmd
}
