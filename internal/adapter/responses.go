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

// TranslateResponsesRequest converts an OpenAI chat-completions body into an
// OpenAI Responses API body. System/developer messages fold into
// `instructions`, the remaining history maps to `input` items, and streaming
// is always forced because the proxy buffers non-stream clients itself.
func TranslateResponsesRequest(body map[string]any, modelName string) ([]byte, string, error) {
	req := make(map[string]any)
	req["model"] = modelName

	var systemTexts []string
	var input []any

	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)

		if role == "system" || role == "developer" {
			if t := systemText(msg["content"]); t != "" {
				systemTexts = append(systemTexts, t)
			}
			continue
		}

		if role == "tool" {
			callID, _ := msg["tool_call_id"].(string)
			if callID == "" {
				return nil, "", fmt.Errorf("tool message without tool_call_id")
			}

			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  systemText(msg["content"]),
			})
			continue
		}

		outRole := "user"
		if role == "assistant" {
			outRole = "assistant"
		}
		item, ok, err := responsesContentItem(msg["content"], outRole)
		if err != nil {
			return nil, "", fmt.Errorf("message content: %w", err)
		}
		if ok {
			input = append(input, item)
		}

		if outRole == "assistant" {
			calls, err := responsesFunctionCallItems(msg["tool_calls"])
			if err != nil {
				return nil, "", fmt.Errorf("tool_calls: %w", err)
			}
			input = append(input, calls...)
		}
	}

	if len(systemTexts) > 0 {
		req["instructions"] = strings.Join(systemTexts, "\n\n")
	}
	if len(input) == 0 {
		return nil, "", fmt.Errorf("no translatable messages in request")
	}

	req["input"] = input

	if v, ok := body["max_tokens"]; ok {
		req["max_output_tokens"] = intValue(v, 0)
	}
	if v, ok := body["max_completion_tokens"]; ok {
		req["max_output_tokens"] = intValue(v, 0)
	}
	for _, key := range []string{"temperature", "top_p"} {
		if v, ok := body[key]; ok {
			req[key] = v
		}
	}
	if effort, _ := body["reasoning_effort"].(string); effort != "" && effort != "none" {
		req["reasoning"] = map[string]any{"effort": effort}
	}
	if tools, ok := body["tools"].([]any); ok {
		if flat := responsesTools(tools); len(flat) > 0 {
			req["tools"] = flat
		}
	}
	if tc, ok := body["tool_choice"]; ok {
		if choice := responsesToolChoice(tc); choice != nil {
			req["tool_choice"] = choice
		}
	}

	req["stream"] = true

	data, err := json.Marshal(req)
	return data, "/v1/responses", err
}

// responsesContentItem converts one chat message content into a single
// Responses input item. Text parts become input_text (user) / output_text
// (assistant), image parts keep their data/http URL as input_image, and plain
// string content is kept verbatim. ok is false when nothing translatable
// remains and no item should be emitted.
func responsesContentItem(content any, role string) (map[string]any, bool, error) {
	if content == nil {
		return nil, false, nil
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return nil, false, nil
		}
		return map[string]any{"role": role, "content": s}, true, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, false, fmt.Errorf("content must be a string or array")
	}

	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}

	var out []any
	for _, p := range parts {
		block, ok := p.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("content part must be an object")
		}
		typeName, _ := block["type"].(string)
		switch typeName {
		case "text", "input_text", "output_text":
			if t, _ := block["text"].(string); t != "" {
				out = append(out, map[string]any{"type": textType, "text": t})
			}
		case "image_url":
			image, ok := block["image_url"].(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("image_url must be an object")
			}
			if ref, _ := image["url"].(string); ref != "" {
				out = append(out, map[string]any{"type": "input_image", "image_url": ref})
			}
		case "input_image":
			ref, _ := block["image_url"].(string)
			if ref == "" {
				if image, ok := block["image_url"].(map[string]any); ok {
					ref, _ = image["url"].(string)
				}
			}
			if ref != "" {
				out = append(out, map[string]any{"type": "input_image", "image_url": ref})
			}
		default:
			return nil, false, fmt.Errorf("unsupported content part type %q", typeName)
		}
	}
	if len(out) == 0 {
		return nil, false, nil
	}

	return map[string]any{"role": role, "content": out}, true, nil
}

// responsesFunctionCallItems converts chat tool_calls into Responses
// function_call input items, one per call, emitted after the assistant text
// item.
func responsesFunctionCallItems(raw any) ([]any, error) {
	tcs, ok := raw.([]any)
	if !ok {
		return nil, nil
	}

	var items []any
	for _, tc := range tcs {
		tcm, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		id, _ := tcm["id"].(string)
		fn, _ := tcm["function"].(map[string]any)
		name, _ := fn["name"].(string)

		var args string
		switch a := fn["arguments"].(type) {
		case string:
			args = a
		case nil:
		default:
			if b, err := json.Marshal(a); err == nil {
				args = string(b)
			}
		}

		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   id,
			"name":      name,
			"arguments": args,
		})
	}
	return items, nil
}

// responsesTools flattens chat function tools {"type":"function",
// "function":{...}} into the Responses tool shape and passes non-function
// tool types through unchanged.
func responsesTools(tools []any) []any {
	var out []any
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if typeName, _ := tm["type"].(string); typeName != "" && typeName != "function" {
			out = append(out, tm)
			continue
		}
		fn, _ := tm["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}

		flat := map[string]any{"type": "function", "name": name}
		if d, ok := fn["description"].(string); ok && d != "" {
			flat["description"] = d
		}
		if p, ok := fn["parameters"]; ok && p != nil {
			flat["parameters"] = p
		}
		out = append(out, flat)
	}
	return out
}

// responsesToolChoice passes string choices through and flattens the chat
// {"type":"function","function":{"name":X}} shape to
// {"type":"function","name":X}.
func responsesToolChoice(tc any) any {
	switch v := tc.(type) {
	case string:
		return v
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]any{"type": "function", "name": name}
			}
		}
		return v
	}
	return tc
}

// responsesEvent is one SSE payload of a Responses stream. Only the fields
// the chat-completions mapping needs are decoded; unknown events are ignored.
type responsesEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`

	Response *struct {
		ID                string `json:"id"`
		CreatedAt         int64  `json:"created_at"`
		Created           int64  `json:"created"`
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	} `json:"response"`

	Item *struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	} `json:"item"`

	Delta string `json:"delta"`
}

// createdUnix prefers created_at and falls back to created for relays that
// reshape the payload.
func (e *responsesEvent) createdUnix() int64 {
	if e.Response == nil {
		return 0
	}
	if e.Response.CreatedAt != 0 {
		return e.Response.CreatedAt
	}
	return e.Response.Created
}

func (e *responsesEvent) errorMessage() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Response != nil && e.Response.Error != nil {
		return e.Response.Error.Message
	}
	return ""
}

func (e *responsesEvent) usageJSON() json.RawMessage {
	if e.Response == nil || e.Response.Usage == nil {
		return nil
	}
	u := e.Response.Usage
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return json.RawMessage(fmt.Sprintf(
		`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`,
		u.InputTokens, u.OutputTokens, total,
	))
}

// responsesChunk mirrors model.StreamChunk but keeps the delta as a raw map:
// tool-call deltas need "index" always present and id/type/name omitted on
// argument-only deltas, which model.ToolCall cannot express.
type responsesChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []responsesChoice `json:"choices"`
	Usage   json.RawMessage   `json:"usage,omitempty"`
}

type responsesChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

// responsesFinishReason maps a terminal Responses event to an OpenAI
// finish_reason: "length" when the response hit max_output_tokens.
func responsesFinishReason(ev *responsesEvent) string {
	if ev.Response != nil && ev.Response.Status == "incomplete" &&
		ev.Response.IncompleteDetails != nil && ev.Response.IncompleteDetails.Reason == "max_output_tokens" {
		return "length"
	}
	return "stop"
}

// BufferResponsesToOpenAI drains a Responses SSE stream and assembles a
// single OpenAI chat.completion JSON document.
func BufferResponsesToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	var (
		msgID     string
		created   = time.Now().Unix()
		content   strings.Builder
		reasoning strings.Builder
		toolCalls []model.ToolCall
		finish    string
		usage     json.RawMessage
	)

	_, err := util.IterDataLines(src, func(payload string) bool {
		var ev responsesEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return true
		}

		switch ev.Type {
		case "response.created":
			if ev.Response != nil {
				if ev.Response.ID != "" {
					msgID = ev.Response.ID
				}
				if ts := ev.createdUnix(); ts != 0 {
					created = ts
				}
			}
		case "response.output_text.delta":
			content.WriteString(ev.Delta)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			reasoning.WriteString(ev.Delta)
		case "response.output_item.added":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				return true
			}

			toolCalls = append(toolCalls, model.ToolCall{
				Index:    len(toolCalls),
				ID:       ev.Item.CallID,
				Type:     "function",
				Function: model.ToolCallFunction{Name: ev.Item.Name},
			})
		case "response.function_call_arguments.delta":
			if len(toolCalls) > 0 {
				last := &toolCalls[len(toolCalls)-1]
				last.Function.Arguments += ev.Delta
			}
		case "response.completed":
			finish = responsesFinishReason(&ev)
			usage = ev.usageJSON()
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	if msgID == "" && content.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 {
		return json.Marshal(map[string]any{"object": "chat.completion", "choices": []any{}})
	}

	for i := range toolCalls {
		if strings.TrimSpace(toolCalls[i].Function.Arguments) == "" {
			toolCalls[i].Function.Arguments = "{}"
		}
	}
	if finish == "" {
		finish = "stop"
	}

	msg := model.Message{Role: "assistant", Content: content.String(), ToolCalls: toolCalls}
	if reasoning.Len() > 0 {
		msg.ReasoningContent = reasoning.String()
	}

	result := model.ChatCompletionResponse{
		ID:      msgID,
		Object:  "chat.completion",
		Created: created,
		Model:   modelName,
		Choices: []model.Choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   usage,
	}
	return json.Marshal(result)
}

// StreamResponsesToOpenAI converts an OpenAI Responses SSE stream into OpenAI
// chat.completion.chunk SSE frames.
func StreamResponsesToOpenAI(src io.Reader, dst io.Writer, modelName string) error {
	flusher, _ := dst.(http.Flusher)
	created := time.Now().Unix()

	msgID := ""
	sentRole := false
	toolIdx := 0
	curToolIdx := 0

	writeChunk := func(delta map[string]any, finish *string, usage json.RawMessage) {
		if msgID == "" {
			msgID = fmt.Sprintf("chatcmpl-responses-%d", created)
		}
		data, _ := json.Marshal(responsesChunk{
			ID:      msgID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   modelName,
			Choices: []responsesChoice{{Index: 0, Delta: delta, FinishReason: finish}},
			Usage:   usage,
		})
		fmt.Fprintf(dst, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeRole := func() {
		if sentRole {
			return
		}
		sentRole = true
		writeChunk(map[string]any{"role": "assistant"}, nil, nil)
	}

	var eventErr error
	_, err := util.IterDataLines(src, func(payload string) bool {
		var ev responsesEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return true
		}

		switch ev.Type {
		case "response.created":
			if ev.Response != nil {
				if ev.Response.ID != "" {
					msgID = ev.Response.ID
				}
				if ts := ev.createdUnix(); ts != 0 {
					created = ts
				}
			}
			writeRole()
		case "response.output_text.delta":
			if ev.Delta == "" {
				return true
			}
			writeRole()
			writeChunk(map[string]any{"content": ev.Delta}, nil, nil)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta == "" {
				return true
			}
			writeRole()
			writeChunk(map[string]any{"reasoning_content": ev.Delta}, nil, nil)
		case "response.output_item.added":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				return true
			}

			writeRole()
			writeChunk(map[string]any{"tool_calls": []any{map[string]any{
				"index":    toolIdx,
				"id":       ev.Item.CallID,
				"type":     "function",
				"function": map[string]any{"name": ev.Item.Name, "arguments": ""},
			}}}, nil, nil)
			curToolIdx = toolIdx
			toolIdx++
		case "response.function_call_arguments.delta":
			if ev.Delta == "" {
				return true
			}
			writeChunk(map[string]any{"tool_calls": []any{map[string]any{
				"index":    curToolIdx,
				"function": map[string]any{"arguments": ev.Delta},
			}}}, nil, nil)
		case "response.completed":
			writeRole()
			finish := responsesFinishReason(&ev)
			writeChunk(map[string]any{}, &finish, nil)
			if u := ev.usageJSON(); u != nil {
				writeChunk(map[string]any{}, nil, u)
			}
			return false
		case "response.failed", "error":
			msg := ev.errorMessage()
			if msg == "" {
				msg = "unknown error"
			}
			eventErr = fmt.Errorf("responses upstream error: %s", msg)
			return false
		}
		return true
	})
	if eventErr != nil {
		return eventErr
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(dst, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// StreamResponsesToAnthropicSSE converts a Responses SSE stream into Anthropic
// SSE by routing it through the OpenAI chunk shape and the existing converter.
func StreamResponsesToAnthropicSSE(src io.Reader, dst http.ResponseWriter, modelName string) error {
	pr, pw := io.Pipe()
	go func() {
		err := StreamResponsesToOpenAI(src, pw, modelName)
		pw.CloseWithError(err)
	}()

	StreamOpenAIToAnthropicSSE(pr, dst, modelName)
	return nil
}
