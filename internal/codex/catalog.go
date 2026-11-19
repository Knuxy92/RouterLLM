package codex

import (
	"encoding/json"
	"fmt"
)

// ParseModels extracts deduplicated model slugs from a Codex catalog response.
func ParseModels(body []byte) ([]string, error) {
	var payload struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("codex: parse models catalog: %w", err)
	}

	seen := make(map[string]struct{}, len(payload.Models))
	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		if model.Slug == "" {
			continue
		}
		if _, ok := seen[model.Slug]; ok {
			continue
		}

		seen[model.Slug] = struct{}{}
		models = append(models, model.Slug)
	}

	return models, nil
}
