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
		Short: "Обнаруженные CLI агентов: версии и модели",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf(" %-13s %-22s %-11s %s\n", "АГЕНТ", "ВЕРСИЯ", "СТАТУС", "МОДЕЛИ")
			for _, a := range agents.Detect() {
				status, extra := "✔ найден", strings.Join(a.Models, " · ")
				if !a.Available {
					status, extra = "✗ не найден", "установка: "+a.Install
				}
				fmt.Printf(" %-13s %-22s %-11s %s\n", a.ID, orDash(a.Version), status, extra)
			}
			return nil
		},
	}
}

func newTaskRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Назначение агентов на роли workflow",
	}
	cmd.AddCommand(newRoleListCmd(), newRoleSetCmd())
	return cmd
}

func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Роли и назначенные агенты/модели",
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
			fmt.Printf(" %-12s %-13s %-26s %s\n", "РОЛЬ", "АГЕНТ", "МОДЕЛЬ", "")
			for _, name := range sortedRoleNames(ws.WF.Roles) {
				r := ws.WF.Roles[name]
				note := ""
				if !detected[r.Agent] {
					note = "⚠ агент не найден в системе"
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
		Use:   "set <роль>",
		Short: "Назначить агента/модель на роль (пишет в workflow.yaml)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			role := args[0]
			if _, ok := ws.WF.Roles[role]; !ok {
				return fmt.Errorf("роль %q не объявлена в workflow.yaml", role)
			}
			if agent == "" {
				return fmt.Errorf("укажи --agent (см. takos agents)")
			}
			if !force {
				info, known := agents.ByID(agent)
				if !known {
					return fmt.Errorf("агент %q не поддерживается (takos agents)", agent)
				}
				if !info.Available {
					return fmt.Errorf("агент %q не найден в системе (takos agents; --force чтобы записать для другой машины)", agent)
				}
			}
			if err := setRoleInYAML(ws.Root, role, agent, model); err != nil {
				return err
			}
			if model != "" {
				ok("%s → %s · %s (записано в workflow.yaml)", role, agent, model)
			} else {
				ok("%s → %s (записано в workflow.yaml)", role, agent)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "id агента из takos agents")
	cmd.Flags().StringVar(&model, "model", "", "модель (например ollama/qwen3-coder:30b)")
	cmd.Flags().BoolVar(&force, "force", false, "записать, даже если агент не найден")
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
		return fmt.Errorf("в workflow.yaml нет секции roles")
	}
	roleNode := findMapValue(roles, role)
	if roleNode == nil {
		return fmt.Errorf("роль %q не найдена в workflow.yaml", role)
	}
	setMapValue(roleNode, "agent", agent)
	if model != "" {
		setMapValue(roleNode, "model", model)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	// Перед записью убеждаемся, что файл остался валидным.
	if _, err := core.ParseWorkflow(out); err != nil {
		return fmt.Errorf("правка сделала workflow.yaml невалидным: %w", err)
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
