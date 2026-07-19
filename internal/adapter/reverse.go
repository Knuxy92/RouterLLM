package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
)

type OpenAIToAnthropicResponse struct {
	ID            string                    `json:"id"`
	Type          string                    `json:"type"`
	Role          string                    `json:"role"`
	Content       []OpenAIToAnthropicBlock  `json:"content"`
	Model         string                    `json:"model"`
	StopReason    *string                   `json:"stop_reason"`
	StopSequence  *string                   `json:"stop_sequence"`
	Usage         *OpenAIToAnthropicUsage   `json:"usage,omitempty"`
}

type OpenAIToAnthropicBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type OpenAIToAnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func AnthropicRequestToOpenAI(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	system, _ := body["system"].(string)
	delete(body, "system")

	if system != "" {
		msgs, _ := body["messages"].([]any)
		all := make([]any, 0, len(msgs)+1)
		all = append(all, map[string]any{"role": "system", "content": system})
		all = append(all, msgs...)
		body["messages"] = all
	}

	delete(body, "stream")

	return json.Marshal(body)
}

func OpenAIResponseToAnthropic(raw []byte) ([]byte, error) {
	var openai struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int                    `json:"index"`
			Message      map[string]any         `json:"message"`
			FinishReason string                 `json:"finish_reason"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(raw, &openai); err != nil {
		return nil, err
	}
	if len(openai.Choices) == 0 {
		return json.Marshal(OpenAIToAnthropicResponse{
			ID:   openai.ID,
			Type: "message",
			Role: "assistant",
			Content: []OpenAIToAnthropicBlock{},
			Model: openai.Model,
		})
	}

	msg := openai.Choices[0].Message
	content, _ := msg["content"].(string)
	reasoning, _ := msg["reasoning_content"].(string)

	var blocks []OpenAIToAnthropicBlock
	if content != "" {
		blocks = append(blocks, OpenAIToAnthropicBlock{Type: "text", Text: content})
	}
	if reasoning != "" {
		blocks = append(blocks, OpenAIToAnthropicBlock{Type: "thinking", Thinking: reasoning})
	}
	if blocks == nil {
		blocks = []OpenAIToAnthropicBlock{}
	}

	sr := mapStopReasonReverse(openai.Choices[0].FinishReason)

	var usage *OpenAIToAnthropicUsage
	if openai.Usage != nil {
		var ou struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		}
		if err := json.Unmarshal(openai.Usage, &ou); err == nil {
			usage = &OpenAIToAnthropicUsage{
				InputTokens:  ou.PromptTokens,
				OutputTokens: ou.CompletionTokens,
			}
		}
	}

	result := OpenAIToAnthropicResponse{
		ID:           openai.ID,
		Type:         "message",
		Role:         "assistant",
		Content:      blocks,
		Model:        openai.Model,
		StopReason:   &sr,
		StopSequence: nil,
		Usage:        usage,
	}

	return json.Marshal(result)
}

func mapStopReasonReverse(reason string) string {
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

func OpenAIStreamToAnthropicSSE(raw []byte, modelName string) []byte {
	var chunk struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int            `json:"index"`
			Delta        map[string]any `json:"delta"`
			FinishReason *string        `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return raw
	}
	if len(chunk.Choices) == 0 {
		return raw
	}
	delta := chunk.Choices[0].Delta
	role, _ := delta["role"].(string)
	content, _ := delta["content"].(string)
	reasoning, _ := delta["reasoning_content"].(string)
	fr := chunk.Choices[0].FinishReason

	var events []string

	if role == "assistant" {
		evt, _ := json.Marshal(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":      chunk.ID,
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
				"model":   modelName,
			},
		})
		events = append(events, fmt.Sprintf("event: message_start\ndata: %s\n\n", evt))
	}

	if content != "" {
		evt, _ := json.Marshal(map[string]any{
			"type": "content_block_delta",
			"index": 0,
			"delta": map[string]string{
				"type": "text_delta",
				"text": content,
			},
		})
		events = append(events, fmt.Sprintf("data: %s\n\n", evt))
	}
	if reasoning != "" {
		evt, _ := json.Marshal(map[string]any{
			"type": "content_block_delta",
			"index": 0,
			"delta": map[string]string{
				"type":     "thinking_delta",
				"thinking": reasoning,
			},
		})
		events = append(events, fmt.Sprintf("data: %s\n\n", evt))
	}
	if fr != nil {
		sr := mapStopReasonReverse(*fr)
		evt, _ := json.Marshal(map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   sr,
				"stop_sequence": nil,
			},
		})
		events = append(events, fmt.Sprintf("data: %s\n\n", evt))
		events = append(events, "event: message_stop\ndata: {}\n\n")
	}

	if len(events) == 0 {
		return raw
	}
	return []byte(strings.Join(events, ""))
}
