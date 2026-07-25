package core

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/busovikov/takos/internal/fsstore"
)

// SetRoleAssignment edits roles.<role>.{agent,model} through the yaml.Node
// tree, preserving comments and formatting of the rest of workflow.yaml.
// The file is re-validated before being written back.
func SetRoleAssignment(root, role, agent, model string) error {
	path := filepath.Join(root, WorkflowFile)
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
	if _, err := ParseWorkflow(out); err != nil {
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
