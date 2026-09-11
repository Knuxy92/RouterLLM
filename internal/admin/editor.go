package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"routerllm/internal/config"
)

type Editor struct {
	path string
	mu   sync.Mutex
}

func NewEditor(path string) *Editor {
	return &Editor{path: path}
}

func (e *Editor) Path() string {
	return e.path
}

func (e *Editor) SetProviderDisabled(name string, disabled bool) error {
	return e.mutate(func(root *yaml.Node) error {
		providers, err := sequenceField(root, "providers")
		if err != nil {
			return err
		}

		entry := findByScalarField(providers, "name", name)
		if entry == nil {
			return fmt.Errorf("provider %q not found in %s", name, e.path)
		}

		setBoolField(entry, "disabled", disabled)

		return nil
	})
}

func (e *Editor) SetRouteDisabled(modelID string, index int, disabled bool) error {
	return e.mutate(func(root *yaml.Node) error {
		entries, err := routeEntries(root, modelID, e.path)
		if err != nil {
			return err
		}
		if index < 0 || index >= len(entries.Content) {
			return fmt.Errorf("route index %d out of range for model %q", index, modelID)
		}

		setBoolField(entries.Content[index], "disabled", disabled)

		return nil
	})
}

// SetModelDisabled parks the whole chain: the model disappears from /v1/models
// and requests for it 404, while its yaml block (and every leg) stays intact.
func (e *Editor) SetModelDisabled(modelID string, disabled bool) error {
	return e.mutate(func(root *yaml.Node) error {
		routes, err := sequenceField(root, "routes")
		if err != nil {
			return err
		}

		rule := findByScalarField(routes, "model_id", modelID)
		if rule == nil {
			return fmt.Errorf("model %q not found in %s", modelID, e.path)
		}

		setBoolField(rule, "disabled", disabled)

		return nil
	})
}

func (e *Editor) MoveRoute(modelID string, index int, up bool) error {
	return e.mutate(func(root *yaml.Node) error {
		entries, err := routeEntries(root, modelID, e.path)
		if err != nil {
			return err
		}

		target := index - 1
		if !up {
			target = index + 1
		}
		if index < 0 || index >= len(entries.Content) {
			return fmt.Errorf("route index %d out of range for model %q", index, modelID)
		}
		if target < 0 || target >= len(entries.Content) {
			return fmt.Errorf("cannot move route %d of model %q past the end of the chain", index, modelID)
		}

		entries.Content[index], entries.Content[target] = entries.Content[target], entries.Content[index]

		return nil
	})
}

// AddRoute appends a fallback leg {provider, model} to the model's chain.
// A non-empty reasoningEffort adds a defaults.reasoning_effort entry; a
// non-empty styleCall pins the leg's wire dialect; disabled=true marks the
// new leg parked from birth.
func (e *Editor) AddRoute(modelID, provider, model, reasoningEffort, styleCall string, disabled bool) error {
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required to add a route to %q", modelID)
	}
	if reasoningEffort != "" && !validReasoningEffort[reasoningEffort] {
		return fmt.Errorf("invalid reasoning_effort %q", reasoningEffort)
	}
	if styleCall != "" && !validStyleCall[styleCall] {
		return fmt.Errorf("invalid stylecall %q (must be chat, responses, or messages)", styleCall)
	}

	return e.mutate(func(root *yaml.Node) error {
		entries, err := routeEntries(root, modelID, e.path)
		if err != nil {
			return err
		}

		leg := &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "provider"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: provider},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "model"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: model},
			},
		}
		if styleCall != "" {
			leg.Content = append(leg.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "stylecall"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: styleCall},
			)
		}
		if reasoningEffort != "" {
			leg.Content = append(leg.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "defaults"},
				&yaml.Node{
					Kind: yaml.MappingNode,
					Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Tag: "!!str", Value: "reasoning_effort"},
						{Kind: yaml.ScalarNode, Tag: "!!str", Value: reasoningEffort},
					},
				},
			)
		}
		if disabled {
			setBoolField(leg, "disabled", true)
		}

		entries.Content = append(entries.Content, leg)

		return nil
	})
}

// RemoveRoute deletes the leg at index. The chain must keep at least one leg —
// a model with an empty routes list would 404 and vanish from /v1/models.
func (e *Editor) RemoveRoute(modelID string, index int) error {
	return e.mutate(func(root *yaml.Node) error {
		entries, err := routeEntries(root, modelID, e.path)
		if err != nil {
			return err
		}
		if index < 0 || index >= len(entries.Content) {
			return fmt.Errorf("route index %d out of range for model %q", index, modelID)
		}
		if len(entries.Content) == 1 {
			return fmt.Errorf("cannot remove the last leg of %q — disable it or delete the model instead", modelID)
		}

		entries.Content = append(entries.Content[:index], entries.Content[index+1:]...)

		return nil
	})
}

var validReasoningEffort = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

var validStyleCall = map[string]bool{
	"chat": true, "responses": true, "messages": true,
}

func effortWhitelist() string {
	quoted := make([]string, 0, len(validReasoningEffort))
	for effort := range validReasoningEffort {
		quoted = append(quoted, fmt.Sprintf("%q", effort))
	}
	sort.Strings(quoted)

	return strings.Join(quoted, ", ")
}

func styleCallWhitelist() string {
	quoted := make([]string, 0, len(validStyleCall))
	for style := range validStyleCall {
		quoted = append(quoted, fmt.Sprintf("%q", style))
	}
	sort.Strings(quoted)

	return strings.Join(quoted, ", ")
}

func (e *Editor) mutate(edit func(*yaml.Node) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	original, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return fmt.Errorf("cannot parse %s: %w", e.path, err)
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("%s is empty", e.path)
	}

	if err := edit(doc.Content[0]); err != nil {
		return err
	}

	encoded, err := encodeDocument(&doc)
	if err != nil {
		return err
	}

	if err := config.ValidateBytes(encoded); err != nil {
		return fmt.Errorf("edited config would not load, file left unchanged: %w", err)
	}

	if err := os.WriteFile(e.path+".bak", original, 0o600); err != nil {
		return fmt.Errorf("cannot write backup: %w", err)
	}

	return writeAtomic(e.path, encoded)
}

func encodeDocument(doc *yaml.Node) ([]byte, error) {
	var out []byte
	buf := &byteBuffer{}
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)

	if err := enc.Encode(doc.Content[0]); err != nil {
		return nil, fmt.Errorf("cannot encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("cannot finalize config: %w", err)
	}
	out = buf.data

	return out, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return writeInPlace(path, data)
	}

	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return writeInPlace(path, data)
	}

	return nil
}

func writeInPlace(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

type byteBuffer struct {
	data []byte
}

func (b *byteBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)

	return len(p), nil
}
