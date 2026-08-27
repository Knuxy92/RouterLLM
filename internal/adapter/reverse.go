package adapter

import (
	"encoding/json"
	"strings"
)

func AnthropicRequestToOpenAI(raw []byte) ([]byte, error) {
	return AnthropicRequestToOpenAIWithResolver(raw, nil)
}

func AnthropicRequestToOpenAIWithResolver(raw []byte, resolve MediaResolver) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	var systemParts []string

	if sys, ok := body["system"]; ok {
		switch v := sys.(type) {
		case []any:
			for _, item := range v {
				if block, ok := item.(map[string]any); ok {
					if text, _ := block["text"].(string); text != "" {
						systemParts = append(systemParts, text)
					}
				}
			}
		case string:
			if v != "" {
				systemParts = append(systemParts, v)
			}
		}
		delete(body, "system")
	}

	var cleanMsgs []any
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			if role == "system" {
				systemParts = append(systemParts, flattenContent(msg["content"]))
				continue
			}
			if role == "assistant" {
				convertContentToToolCalls(msg)
				stripCacheControl(msg)
				cleanMsgs = append(cleanMsgs, msg)
				continue
			}
			if role == "user" {
				toolResults := extractToolResults(msg["content"])
				if len(toolResults) > 0 {
					for _, tr := range toolResults {
						cleanMsgs = append(cleanMsgs, tr)
					}
					textContent := flattenContent(msg["content"])
					if textContent != "" {
						textMsg := map[string]any{
							"role":    "user",
							"content": textContent,
						}
						stripCacheControl(textMsg)
						cleanMsgs = append(cleanMsgs, textMsg)
					}
					continue
				}
				if blocks := anthropicContentToOpenAI(msg["content"]); len(blocks) > 0 && hasNonTextContent(blocks) {
					msg["content"] = blocks
				} else {
					msg["content"] = flattenContent(msg["content"])
				}
				stripCacheControl(msg)
				cleanMsgs = append(cleanMsgs, msg)
				continue
			}
			stripCacheControl(msg)
			cleanMsgs = append(cleanMsgs, msg)
		}
	}

	if len(systemParts) > 0 {
		cleanMsgs = append([]any{
			map[string]any{
				"role":    "system",
				"content": strings.Join(systemParts, "\n\n"),
			},
		}, cleanMsgs...)
	}

	delete(body, "metadata")

	if tools, ok := body["tools"].([]any); ok {
		converted := make([]any, 0, len(tools))
		for _, t := range tools {
			if convertedTool := convertAnthropicTool(t); convertedTool != nil {
				converted = append(converted, convertedTool)
			}
		}
		if len(converted) > 0 {
			body["tools"] = converted
		} else {
			delete(body, "tools")
		}
	}

	if tc, ok := body["tool_choice"]; ok {
		body["tool_choice"] = convertAnthropicToolChoice(tc)
	}

	body["messages"] = cleanMsgs

	return json.Marshal(body)
}

func convertContentToToolCalls(msg map[string]any) {
	content, ok := msg["content"].([]any)
	if !ok {
		return
	}

	var textParts []string
	var toolCalls []any

	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		btype, _ := b["type"].(string)
		switch btype {
		case "text":
			if t, _ := b["text"].(string); t != "" {
				textParts = append(textParts, t)
			}
		case "tool_use":
			tc := convertToolUseToToolCall(b)
			if tc != nil {
				toolCalls = append(toolCalls, tc)
			}
		case "thinking":
			// pass through in content text
			if t, _ := b["thinking"].(string); t != "" {
				textParts = append(textParts, t)
			}
		}
	}

	msg["content"] = strings.Join(textParts, "")
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
}

func convertToolUseToToolCall(block map[string]any) any {
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	input := block["input"]

	args, err := json.Marshal(input)
	if err != nil {
		args = []byte("{}")
	}

	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": string(args),
		},
	}
}

func extractToolResults(content any) []map[string]any {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	var results []map[string]any
	for _, block := range blocks {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		btype, _ := b["type"].(string)
		if btype != "tool_result" {
			continue
		}
		toolUseID, _ := b["tool_use_id"].(string)
		if toolUseID == "" {
			continue
		}
		trContent := flattenContent(b["content"])

		results = append(results, map[string]any{
			"role":         "tool",
			"tool_call_id": toolUseID,
			"content":      trContent,
		})
	}
	return results
}

func convertAnthropicTool(t any) any {
	tool, ok := t.(map[string]any)
	if !ok {
		return nil
	}
	name, _ := tool["name"].(string)
	if name == "" {
		return nil
	}
	desc, _ := tool["description"].(string)
	inputSchema := tool["input_schema"]

	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": desc,
			"parameters":  inputSchema,
		},
	}
}

func convertAnthropicToolChoice(tc any) any {
	m, ok := tc.(map[string]any)
	if !ok {
		return tc
	}
	ttype, _ := m["type"].(string)
	switch ttype {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		if name, _ := m["name"].(string); name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}
		}
		return "auto"
	default:
		return tc
	}
}

func flattenContent(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]any); ok {
				if text, _ := m["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func stripCacheControl(msg map[string]any) {
	if content, ok := msg["content"].([]any); ok {
		for _, block := range content {
			if m, ok := block.(map[string]any); ok {
				delete(m, "cache_control")
			}
		}
	}
}

func MapStopReasonReverse(reason string) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	default:
		return reason
	}
}
