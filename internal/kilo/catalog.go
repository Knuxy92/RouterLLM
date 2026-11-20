package kilo

import (
	"encoding/json"
	"fmt"
)

func ParseModels(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse kilo models catalog: %w", err)
	}

	seen := make(map[string]bool, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if entry.ID == "" || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		models = append(models, entry.ID)
	}

	return models, nil
}
