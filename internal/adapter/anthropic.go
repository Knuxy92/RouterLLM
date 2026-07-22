package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
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
			maps.Copy(clampedThinking, thinking)

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
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
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

type anthropicSSEState struct {
	msgID          string
	sentFirstChunk bool
	blockStarted   bool
	prevBlockType  string
	blockIndex     int
	toolStates     map[int]*toolStreamState
	toolOrder      []int
	lastUsage      json.RawMessage
}

type toolStreamState struct {
	id          string
	name        string
	argsBuf     strings.Builder
	lastEmitLen int
	started     bool
	blockIndex  int
}

func (ts *toolStreamState) freshArgs() string {
	s := ts.argsBuf.String()
	if ts.lastEmitLen >= len(s) {
		return ""
	}
	part := s[ts.lastEmitLen:]
	ts.lastEmitLen = len(s)
	return part
}

// StreamOpenAIToAnthropicSSE reads OpenAI SSE chunks from src and writes
// proper Anthropic SSE events (with event: prefix) to dst.
func StreamOpenAIToAnthropicSSE(src io.Reader, dst http.ResponseWriter, modelName string) {
	flusher, _ := dst.(http.Flusher)
	var st anthropicSSEState

	writeSSE := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(dst, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	stopBlock := func() {
		writeSSE("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": st.blockIndex,
		})
		st.blockStarted = false
	}
	closeTextBlock := func() {
		if st.blockStarted {
			stopBlock()
			st.blockIndex++
		}
	}
	startBlock := func(blockType string) {
		if st.blockStarted && st.prevBlockType != blockType {
			stopBlock()
			st.blockIndex++
		}
		if st.blockStarted {
			return
		}

		st.blockStarted = true
		st.prevBlockType = blockType
		contentBlock := map[string]any{"type": "text", "text": ""}
		if blockType == "thinking" {
			contentBlock = map[string]any{"type": "thinking", "thinking": ""}
		}
		writeSSE("content_block_start", map[string]any{
			"type": "content_block_start", "index": st.blockIndex,
			"content_block": contentBlock,
		})
	}

	_, _ = util.IterDataLines(src, func(payload string) bool {
		var chunk struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Model   string `json:"model"`
			Choices []struct {
				Index        int            `json:"index"`
				Delta        map[string]any `json:"delta"`
				FinishReason *string        `json:"finish_reason"`
			} `json:"choices"`
			Usage json.RawMessage `json:"usage,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return true
		}

		if len(chunk.Choices) == 0 {
			if len(chunk.Usage) > 0 {
				st.lastUsage = chunk.Usage
			}
			return true
		}

		if len(chunk.Usage) > 0 {
			st.lastUsage = chunk.Usage
		}

		if len(chunk.Choices) > 1 {
			log.Printf("warning: /v1/chat/completions returned %d choices, using only choices[0]", len(chunk.Choices))
		}

		if st.msgID == "" && chunk.ID != "" {
			st.msgID = chunk.ID
		}

		delta := chunk.Choices[0].Delta
		content, _ := delta["content"].(string)
		reasoning, _ := delta["reasoning_content"].(string)
		fr := chunk.Choices[0].FinishReason

		tcs, hasTC := delta["tool_calls"].([]any)

		if !st.sentFirstChunk {
			st.sentFirstChunk = true
			writeSSE("message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":      st.msgID,
					"type":    "message",
					"role":    "assistant",
					"content": []any{},
					"model":   modelName,
				},
			})
		}

		if fr != nil && !st.blockStarted && content == "" && reasoning == "" && (!hasTC || len(tcs) == 0) && len(st.toolStates) == 0 {
			writeSSE("content_block_start", map[string]any{
				"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			writeSSE("content_block_stop", map[string]any{
				"type": "content_block_stop", "index": 0,
			})
			writeSSE("message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": MapStopReasonReverse(*fr), "stop_sequence": nil},
			})
			writeSSE("message_stop", map[string]any{"type": "message_stop"})
			return false
		}

		if reasoning != "" {
			startBlock("thinking")
			writeSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": st.blockIndex,
				"delta": map[string]string{"type": "thinking_delta", "thinking": reasoning},
			})
		}
		if content != "" {
			startBlock("text")
			writeSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": st.blockIndex,
				"delta": map[string]string{"type": "text_delta", "text": content},
			})
		}

		if hasTC && len(tcs) > 0 {
			closeTextBlock()

			for _, tc := range tcs {
				tcm, ok := tc.(map[string]any)
				if !ok {
					continue
				}
				tidx := 0
				if fi, ok := tcm["index"].(float64); ok {
					tidx = int(fi)
				}

				ts, exists := st.toolStates[tidx]
				if !exists {
					ts = &toolStreamState{}
					if st.toolStates == nil {
						st.toolStates = make(map[int]*toolStreamState)
					}
					st.toolStates[tidx] = ts
					st.toolOrder = append(st.toolOrder, tidx)
				}

				if id, ok := tcm["id"].(string); ok && id != "" {
					ts.id = id
				}
				if fn, ok := tcm["function"].(map[string]any); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						ts.name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						ts.argsBuf.WriteString(args)
					}
				}

				if !ts.started && ts.id != "" && ts.name != "" {
					ts.started = true
					ts.blockIndex = st.blockIndex
					st.blockIndex++
					writeSSE("content_block_start", map[string]any{
						"type": "content_block_start", "index": ts.blockIndex,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   ts.id,
							"name": ts.name,
						},
					})
				}

				if ts.started {
					if part := ts.freshArgs(); part != "" {
						writeSSE("content_block_delta", map[string]any{
							"type": "content_block_delta", "index": ts.blockIndex,
							"delta": map[string]string{"type": "input_json_delta", "partial_json": part},
						})
					}
				}
			}
		}

		if fr != nil {
			if st.blockStarted {
				stopBlock()
			}

			for _, tidx := range st.toolOrder {
				ts := st.toolStates[tidx]
				if ts.started {
					writeSSE("content_block_stop", map[string]any{
						"type": "content_block_stop", "index": ts.blockIndex,
					})
				} else if ts.id != "" && ts.name != "" {
					ts.blockIndex = st.blockIndex
					st.blockIndex++
					writeSSE("content_block_start", map[string]any{
						"type": "content_block_start", "index": ts.blockIndex,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   ts.id,
							"name": ts.name,
						},
					})
					if part := ts.freshArgs(); part != "" {
						writeSSE("content_block_delta", map[string]any{
							"type": "content_block_delta", "index": ts.blockIndex,
							"delta": map[string]string{"type": "input_json_delta", "partial_json": part},
						})
					}
					writeSSE("content_block_stop", map[string]any{
						"type": "content_block_stop", "index": ts.blockIndex,
					})
				}
			}

			msgDelta := map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": MapStopReasonReverse(*fr), "stop_sequence": nil},
			}
			if st.lastUsage != nil {
				msgDelta["usage"] = st.lastUsage
			}
			writeSSE("message_delta", msgDelta)
			writeSSE("message_stop", map[string]any{"type": "message_stop"})
			return false
		}
		return true
	})
}
