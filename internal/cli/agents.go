package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/busovikov/takos/internal/agents"
	"github.com/busovikov/takos/internal/core"
	"github.com/busovikov/takos/internal/fsstore"
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
			if err := setRoleInYAML(ws.Root, role, agent, model); err != nil {
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

// setRoleInYAML edits roles.<role>.{agent,model} through the yaml.Node tree,
// preserving comments and formatting of the rest of workflow.yaml.
func setRoleInYAML(root, role, agent, model string) error {
	path := filepath.Join(root, core.WorkflowFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	roles := findMapValue(doc.Content[0], "roles")
	if roles == nil {
		return fmt.Errorf("workflow.yaml has no roles section")
	}
	roleNode := findMapValue(roles, role)
	if roleNode == nil {
		return fmt.Errorf("role %q not found in workflow.yaml", role)
	}
	setMapValue(roleNode, "agent", agent)
	if model != "" {
		setMapValue(roleNode, "model", model)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	// Make sure the file is still valid before writing it back.
	if _, err := core.ParseWorkflow(out); err != nil {
		return fmt.Errorf("the edit made workflow.yaml invalid: %w", err)
	}
	return fsstore.WriteFileAtomic(path, out, 0o644)
}

func findMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setMapValue(mapping *yaml.Node, key, value string) {
	if node := findMapValue(mapping, key); node != nil {
		node.SetString(value)
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value})
}
