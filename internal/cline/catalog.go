package cline

import (
	"encoding/json"
	"fmt"
)

var modelBuckets = []string{"clinePass", "free", "clineCloud"}

func ParseModels(body []byte) ([]string, error) {
	var buckets map[string][]struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &buckets); err != nil {
		return nil, fmt.Errorf("parse cline recommended models: %w", err)
	}

	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, bucket := range modelBuckets {
		for _, entry := range buckets[bucket] {
			if entry.ID == "" || seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			models = append(models, entry.ID)
		}
	}

	return models, nil
}
