package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentrouter/internal/model"
	"agentrouter/internal/util"
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
				if content, ok := msg["content"].(string); ok {
					systemParts = append(systemParts, content)
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

	if mt, ok := body["max_tokens"]; ok {
		if f, ok := mt.(float64); ok {
			req["max_tokens"] = int(f)
		} else {
			req["max_tokens"] = mt
		}
	} else {
		req["max_tokens"] = 4096
	}

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

func TranslateResponse(anthropicBody []byte) ([]byte, error) {
	var ar struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(anthropicBody, &ar); err != nil {
		return nil, err
	}

	var contentBuilder strings.Builder
	for _, block := range ar.Content {
		if block.Type == "text" {
			contentBuilder.WriteString(block.Text)
		}
	}

	result := model.ChatCompletionResponse{
		ID:      ar.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ar.Model,
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.Message{Role: "assistant", Content: contentBuilder.String()},
			FinishReason: mapStopReason(ar.StopReason),
		}},
	}

	if ar.Usage != nil {
		result.Usage = json.RawMessage(fmt.Sprintf(
			`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`,
			ar.Usage.InputTokens, ar.Usage.OutputTokens, ar.Usage.InputTokens+ar.Usage.OutputTokens,
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
