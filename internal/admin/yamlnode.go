package admin

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func sequenceField(root *yaml.Node, key string) (*yaml.Node, error) {
	value := mappingValue(root, key)
	if value == nil || value.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("config has no %s list", key)
	}

	return value, nil
}

func findByScalarField(sequence *yaml.Node, field, want string) *yaml.Node {
	for _, entry := range sequence.Content {
		if scalar := mappingValue(entry, field); scalar != nil && scalar.Value == want {
			return entry
		}
	}

	return nil
}

func routeEntries(root *yaml.Node, modelID, path string) (*yaml.Node, error) {
	routes, err := sequenceField(root, "routes")
	if err != nil {
		return nil, err
	}

	rule := findByScalarField(routes, "model_id", modelID)
	if rule == nil {
		return nil, fmt.Errorf("model %q not found in %s", modelID, path)
	}

	entries := mappingValue(rule, "routes")
	if entries == nil || entries.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("model %q has no routes list", modelID)
	}

	return entries, nil
}

func setBoolField(node *yaml.Node, key string, value bool) {
	literal := "false"
	if value {
		literal = "true"
	}

	if existing := mappingValue(node, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = "!!bool"
		existing.Value = literal
		existing.Style = 0

		return
	}

	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: literal},
	)
}
