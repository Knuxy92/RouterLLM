package dto

import "encoding/json"

func ParseAndForceStream(raw []byte) (map[string]any, bool, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	clientStream, _ := body["stream"].(bool)
	body["stream"] = true
	return body, clientStream, nil
}
