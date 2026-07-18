package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"routerllm/internal/model"
	"routerllm/internal/util"
)

func TranslateRequest(body map[string]any, modelName string) ([]byte, string, error) {
	req := make(map[string]any)
	req["model"] = modelName

	var systemParts []string
	var messages []any
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			if role == "system" {
				if t := systemText(msg["content"]); t != "" {
					systemParts = append(systemParts, t)
				}
				continue
			}
			messages = append(messages, msg)
		}
	}
	if len(systemParts) > 0 {
		req["system"] = strings.Join(systemParts, "\n\n")
	}
	req["messages"] = messages

	maxTokens := 4096
	explicitMaxTokens := false
	if mt, ok := body["max_tokens"]; ok {
		maxTokens = intValue(mt, maxTokens)
		explicitMaxTokens = true
	}
	if thinking, ok := body["thinking"].(map[string]any); ok {
		_, hasBudget := thinking["budget_tokens"]
		budget := intValue(thinking["budget_tokens"], 0)
		if budget > 0 && maxTokens <= budget {
			if explicitMaxTokens {
				budget = maxTokens - 1
			} else {
				maxTokens = budget + 1024
			}
		}
		if budget > 0 {
			clampedThinking := make(map[string]any, len(thinking))
			for k, v := range thinking {
				clampedThinking[k] = v
			}
			clampedThinking["budget_tokens"] = budget
			req["thinking"] = clampedThinking
		} else if !hasBudget {
			req["thinking"] = thinking
		}
	}
	req["max_tokens"] = maxTokens

	for _, key := range []string{"temperature", "top_p", "stream"} {
		if v, ok := body[key]; ok {
			req[key] = v
		}
	}
	if stop, ok := body["stop"]; ok {
		req["stop_sequences"] = stop
	}

	req["stream"] = true

	data, err := json.Marshal(req)
	return data, "/v1/messages", err
}

func intValue(v any, fallback int) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return fallback
}

func systemText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, _ := m["text"].(string); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func BufferAnthropicToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	var msgID string
	var upModel string
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var stopReason string
	var usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	_, err := util.IterDataLines(src, func(payload string) bool {
		var event struct {
			Type    string `json:"type"`
			Message *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Delta *struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				Thinking   string `json:"thinking"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return true
		}
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				msgID = event.Message.ID
				upModel = event.Message.Model
			}
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Type == "text_delta" {
				contentBuilder.WriteString(event.Delta.Text)
			}
			if event.Delta != nil && event.Delta.Type == "thinking_delta" {
				reasoningBuilder.WriteString(event.Delta.Thinking)
			}
		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				stopReason = event.Delta.StopReason
			}
			if event.Usage != nil {
				usage = event.Usage
			}
		case "message_stop":
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	msg := model.Message{Role: "assistant", Content: contentBuilder.String()}
	if reasoning := reasoningBuilder.String(); reasoning != "" {
		msg.ReasoningContent = reasoning
	}

	result := model.ChatCompletionResponse{
		ID:      msgID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   upModel,
		Choices: []model.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: mapStopReason(stopReason),
		}},
	}
	if usage != nil {
		result.Usage = json.RawMessage(fmt.Sprintf(
			`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`,
			usage.InputTokens, usage.OutputTokens, usage.InputTokens+usage.OutputTokens,
		))
	}
	return json.Marshal(result)
}

func StreamAnthropicToOpenAI(src io.Reader, dst http.ResponseWriter, modelName string) error {
	flusher, _ := dst.(http.Flusher)
	var msgID string
	created := time.Now().Unix()

	writeChunk := func(chunk model.StreamChunk) {
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(dst, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	_, err := util.IterDataLines(src, func(payload string) bool {
		var event struct {
			Type    string `json:"type"`
			Message *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Delta *struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				Thinking   string `json:"thinking"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return true
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil {
				msgID = event.Message.ID
			}
			writeChunk(model.StreamChunk{
				ID:      msgID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   modelName,
				Choices: []model.StreamChoice{{
					Index:        0,
					Delta:        model.Delta{Role: "assistant"},
					FinishReason: nil,
				}},
			})

		case "content_block_delta":
			if event.Delta != nil && event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				writeChunk(model.StreamChunk{
					ID:      msgID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   modelName,
					Choices: []model.StreamChoice{{
						Index:        0,
						Delta:        model.Delta{Content: event.Delta.Text},
						FinishReason: nil,
					}},
				})
			}
			if event.Delta != nil && event.Delta.Type == "thinking_delta" && event.Delta.Thinking != "" {
				writeChunk(model.StreamChunk{
					ID:      msgID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   modelName,
					Choices: []model.StreamChoice{{
						Index:        0,
						Delta:        model.Delta{ReasoningContent: event.Delta.Thinking},
						FinishReason: nil,
					}},
				})
			}

		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				fr := mapStopReason(event.Delta.StopReason)
				var usage json.RawMessage
				if event.Usage != nil {
					usage = json.RawMessage(fmt.Sprintf(
						`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`,
						event.Usage.InputTokens, event.Usage.OutputTokens,
						event.Usage.InputTokens+event.Usage.OutputTokens,
					))
				}
				writeChunk(model.StreamChunk{
					ID:      msgID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   modelName,
					Choices: []model.StreamChoice{{
						Index:        0,
						Delta:        model.Delta{},
						FinishReason: &fr,
					}},
					Usage: usage,
				})
			}

		case "message_stop":
			return false
		}
		return true
	})

	fmt.Fprintf(dst, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return err
}

func mapStopReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}
