package admin

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Editor struct {
	path string
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

func (e *Editor) mutate(edit func(*yaml.Node) error) error {
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
