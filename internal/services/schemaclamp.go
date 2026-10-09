package services

import (
	"strings"

	"routerllm/internal/config"
)

// Upstream gateways validate every tool definition in a request and reject the
// whole call when one schema nests deeper than their limit — the OpenCode
// gateway states it as "JSON schema exceeds the maximum nesting depth of 10
// levels", and other relays enforce the same ceiling. MCP tool schemas
// routinely exceed that on their own, so the proxy flattens the offending
// subtree before dispatch: property names and descriptions survive, only the
// deep structure below them is replaced by a stub.

// defaultToolSchemaMaxDepth is the depth budget applied when the config does
// not set one. It sits below the gateway's own limit of 10 because the two
// counters do not agree at the edges (properties wrappers, $defs, tuple
// items), and a request that passes locally must still pass upstream.
const defaultToolSchemaMaxDepth = config.DefaultToolSchemaMaxDepth

var (
	// schemaChildKeys hold named sub-schemas, one per entry.
	schemaChildKeys = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

	// schemaBranchKeys hold a schema or a list of alternative schemas.
	schemaBranchKeys = []string{"anyOf", "oneOf", "allOf", "prefixItems"}
)

// clampToolSchemaDepths flattens every tool schema in body that nests deeper
// than maxDepth and returns the names of the tools it touched. Both the chat
// shape (parameters under "function") and the flat Responses shape are
// handled, so the clamp can run once on the inbound body before the dialect
// translation.
func clampToolSchemaDepths(body map[string]any, maxDepth int) []string {
	tools, ok := body["tools"].([]any)
	if !ok || maxDepth <= 0 {
		return nil
	}

	var touched []string

	for _, entry := range tools {
		tool, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		holder := tool
		if fn, ok := tool["function"].(map[string]any); ok {
			holder = fn
		}

		schema, ok := holder["parameters"].(map[string]any)
		if !ok || schemaDepth(schema) <= maxDepth {
			continue
		}

		if !clampSchemaInPlace(schema, 1, maxDepth) {
			continue
		}

		name, _ := holder["name"].(string)
		touched = append(touched, name)
	}

	return touched
}

// schemaDepth reports how many levels a schema nests: the node itself is 1 and
// its properties, items and branch alternatives count as the next level.
// Anything that is not an object is a leaf.
func schemaDepth(schema any) int {
	node, ok := schema.(map[string]any)
	if !ok {
		return 0
	}

	depth := 1

	for _, key := range schemaChildKeys {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for _, child := range children {
			if d := schemaDepth(child) + 1; d > depth {
				depth = d
			}
		}
	}

	switch items := node["items"].(type) {
	case map[string]any:
		if d := schemaDepth(items) + 1; d > depth {
			depth = d
		}
	case []any:
		for _, child := range items {
			if d := schemaDepth(child) + 1; d > depth {
				depth = d
			}
		}
	}

	for _, key := range schemaBranchKeys {
		switch branches := node[key].(type) {
		case map[string]any:
			if d := schemaDepth(branches) + 1; d > depth {
				depth = d
			}
		case []any:
			for _, child := range branches {
				if d := schemaDepth(child) + 1; d > depth {
					depth = d
				}
			}
		}
	}

	return depth
}

// clampSchemaInPlace rewrites every node at or below the budget into a stub
// that keeps the descriptive fields the model reads, and reports whether
// anything moved. Flattening starts one level early so the stub that replaces
// the deepest structured node still counts as the last level.
func clampSchemaInPlace(node map[string]any, depth, maxDepth int) bool {
	if depth >= maxDepth {
		if !hasSubSchemas(node) {
			return false
		}

		stub := flattenedStub(node)
		for key := range node {
			delete(node, key)
		}
		for key, value := range stub {
			node[key] = value
		}

		return true
	}

	changed := false

	for _, key := range schemaChildKeys {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for name, child := range children {
			childNode, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if clampSchemaInPlace(childNode, depth+1, maxDepth) {
				children[name] = childNode
				changed = true
			}
		}
	}

	switch items := node["items"].(type) {
	case map[string]any:
		if clampSchemaInPlace(items, depth+1, maxDepth) {
			changed = true
		}
	case []any:
		for i, child := range items {
			childNode, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if clampSchemaInPlace(childNode, depth+1, maxDepth) {
				items[i] = childNode
				changed = true
			}
		}
	}

	for _, key := range schemaBranchKeys {
		switch branches := node[key].(type) {
		case map[string]any:
			if clampSchemaInPlace(branches, depth+1, maxDepth) {
				changed = true
			}
		case []any:
			for i, child := range branches {
				childNode, ok := child.(map[string]any)
				if !ok {
					continue
				}
				if clampSchemaInPlace(childNode, depth+1, maxDepth) {
					branches[i] = childNode
					changed = true
				}
			}
		}
	}

	return changed
}

// hasSubSchemas reports whether a node carries structure worth flattening. A
// leaf that happens to sit past the budget is left alone.
func hasSubSchemas(node map[string]any) bool {
	for _, key := range schemaChildKeys {
		if _, ok := node[key]; ok {
			return true
		}
	}
	if _, ok := node["items"]; ok {
		return true
	}
	for _, key := range schemaBranchKeys {
		if _, ok := node[key]; ok {
			return true
		}
	}

	return false
}

// flattenedStub keeps the fields that still tell the model what the argument is
// and drops everything structural.
func flattenedStub(node map[string]any) map[string]any {
	stub := map[string]any{}

	for _, key := range []string{"type", "title", "description", "format", "enum", "default"} {
		if value, ok := node[key]; ok {
			stub[key] = value
		}
	}
	if _, ok := stub["type"]; !ok {
		stub["type"] = "object"
	}
	if _, ok := stub["description"]; !ok {
		stub["description"] = "deep structure flattened: exceeds the upstream JSON-schema nesting limit"
	}

	return stub
}

// describeClampedTools renders clamped tool names for the log line, capped so a
// pathological request cannot flood the log.
func describeClampedTools(names []string) string {
	const maxLogged = 5

	if len(names) <= maxLogged {
		return strings.Join(names, ", ")
	}

	return strings.Join(names[:maxLogged], ", ") + ", …"
}