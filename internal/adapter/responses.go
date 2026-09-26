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

	for _, key := range maxTokenKeys {
		if v, ok := body[key]; ok {
			req["max_output_tokens"] = intValue(v, 0)
		}
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
		if s, ok := fn["strict"].(bool); ok {
			flat["strict"] = s
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
	return openAIUsageJSON(int(u.InputTokens), int(u.OutputTokens), int(total))
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

// responsesSink receives the OpenAI-shaped output of the shared Responses
// event core.
type responsesSink interface {
	created(id string, ts int64)
	content(delta string)
	reasoning(delta string)
	toolStart(callID, name string)
	toolArgs(delta string)
	completed(finish string, usage json.RawMessage)
	// failure handles a terminal upstream error event and reports whether
	// iteration should stop.
	failure(message string) bool
}

type responsesBufferSink struct {
	msgID        string
	createdAt    int64
	text         strings.Builder
	reasoningBuf strings.Builder
	toolCalls    []model.ToolCall
	finishReason string
	lastUsage    json.RawMessage
}

func (s *responsesBufferSink) created(id string, ts int64) {
	if id != "" {
		s.msgID = id
	}
	if ts != 0 {
		s.createdAt = ts
	}
}

func (s *responsesBufferSink) content(delta string) {
	s.text.WriteString(delta)
}

func (s *responsesBufferSink) reasoning(delta string) {
	s.reasoningBuf.WriteString(delta)
}

func (s *responsesBufferSink) toolStart(callID, name string) {
	s.toolCalls = append(s.toolCalls, model.ToolCall{
		Index:    len(s.toolCalls),
		ID:       callID,
		Type:     "function",
		Function: model.ToolCallFunction{Name: name},
	})
}

func (s *responsesBufferSink) toolArgs(delta string) {
	if len(s.toolCalls) > 0 {
		last := &s.toolCalls[len(s.toolCalls)-1]
		last.Function.Arguments += delta
	}
}

func (s *responsesBufferSink) completed(finish string, usage json.RawMessage) {
	s.finishReason = finish
	s.lastUsage = usage
}

func (s *responsesBufferSink) failure(string) bool { return true }

func (s *responsesBufferSink) document(modelName string) ([]byte, error) {
	if s.msgID == "" && s.text.Len() == 0 && s.reasoningBuf.Len() == 0 && len(s.toolCalls) == 0 {
		return json.Marshal(map[string]any{"object": model.ChatCompletionObject, "choices": []any{}})
	}

	for i := range s.toolCalls {
		if strings.TrimSpace(s.toolCalls[i].Function.Arguments) == "" {
			s.toolCalls[i].Function.Arguments = "{}"
		}
	}
	finish := s.finishReason
	if finish == "" {
		finish = "stop"
	}

	msg := model.Message{Role: "assistant", Content: s.text.String(), ToolCalls: s.toolCalls}
	if s.reasoningBuf.Len() > 0 {
		msg.ReasoningContent = s.reasoningBuf.String()
	}

	result := model.ChatCompletionResponse{
		ID:      s.msgID,
		Object:  model.ChatCompletionObject,
		Created: s.createdAt,
		Model:   modelName,
		Choices: []model.Choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   s.lastUsage,
	}
	return json.Marshal(result)
}

// emitResponsesAsOpenAI translates a Responses SSE stream into OpenAI deltas
// and hands them to the sink.
func emitResponsesAsOpenAI(src io.Reader, sink responsesSink) error {
	_, err := util.IterDataLines(src, func(payload string) bool {
		var ev responsesEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return true
		}

		switch ev.Type {
		case "response.created":
			var id string
			var ts int64
			if ev.Response != nil {
				id = ev.Response.ID
				ts = ev.createdUnix()
			}
			sink.created(id, ts)
		case "response.output_text.delta":
			if ev.Delta != "" {
				sink.content(ev.Delta)
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta != "" {
				sink.reasoning(ev.Delta)
			}
		case "response.output_item.added":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				return true
			}
			sink.toolStart(ev.Item.CallID, ev.Item.Name)
		case "response.function_call_arguments.delta":
			if ev.Delta != "" {
				sink.toolArgs(ev.Delta)
			}
		case "response.completed":
			sink.completed(responsesFinishReason(&ev), ev.usageJSON())
			return false
		case "response.failed", "error":
			msg := ev.errorMessage()
			if msg == "" {
				msg = "unknown error"
			}
			return sink.failure(msg)
		}
		return true
	})
	return err
}

// BufferResponsesToOpenAI drains a Responses SSE stream and assembles a
// single OpenAI chat.completion JSON document.
func BufferResponsesToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	sink := &responsesBufferSink{createdAt: time.Now().Unix()}
	if err := emitResponsesAsOpenAI(src, sink); err != nil {
		return nil, err
	}
	return sink.document(modelName)
}

type responsesStreamSink struct {
	sse        sseWriter
	msgID      string
	createdAt  int64
	model      string
	sentRole   bool
	toolIdx    int
	curToolIdx int
	err        error
}

func newResponsesStreamSink(dst io.Writer, modelName string) *responsesStreamSink {
	return &responsesStreamSink{
		sse:       newSSEWriter(dst),
		createdAt: time.Now().Unix(),
		model:     modelName,
	}
}

func (s *responsesStreamSink) chunk(delta map[string]any, finish *string, usage json.RawMessage) {
	if s.msgID == "" {
		s.msgID = chatCompletionIDPrefix + fmt.Sprintf("responses-%d", s.createdAt)
	}
	s.sse.frame(responsesChunk{
		ID:      s.msgID,
		Object:  model.ChatCompletionChunkObject,
		Created: s.createdAt,
		Model:   s.model,
		Choices: []responsesChoice{{Index: 0, Delta: delta, FinishReason: finish}},
		Usage:   usage,
	})
}

func (s *responsesStreamSink) emitRole() {
	if s.sentRole {
		return
	}
	s.sentRole = true
	s.chunk(map[string]any{"role": "assistant"}, nil, nil)
}

func (s *responsesStreamSink) created(id string, ts int64) {
	if id != "" {
		s.msgID = id
	}
	if ts != 0 {
		s.createdAt = ts
	}
	s.emitRole()
}

func (s *responsesStreamSink) content(delta string) {
	s.emitRole()
	s.chunk(map[string]any{"content": delta}, nil, nil)
}

func (s *responsesStreamSink) reasoning(delta string) {
	s.emitRole()
	s.chunk(map[string]any{"reasoning_content": delta}, nil, nil)
}

func (s *responsesStreamSink) toolStart(callID, name string) {
	s.emitRole()
	s.chunk(map[string]any{"tool_calls": []any{map[string]any{
		"index":    s.toolIdx,
		"id":       callID,
		"type":     "function",
		"function": map[string]any{"name": name, "arguments": ""},
	}}}, nil, nil)
	s.curToolIdx = s.toolIdx
	s.toolIdx++
}

func (s *responsesStreamSink) toolArgs(delta string) {
	s.chunk(map[string]any{"tool_calls": []any{map[string]any{
		"index":    s.curToolIdx,
		"function": map[string]any{"arguments": delta},
	}}}, nil, nil)
}

func (s *responsesStreamSink) completed(finish string, usage json.RawMessage) {
	s.emitRole()
	s.chunk(map[string]any{}, &finish, nil)
	if usage != nil {
		s.chunk(map[string]any{}, nil, usage)
	}
}

func (s *responsesStreamSink) failure(message string) bool {
	s.err = fmt.Errorf("responses upstream error: %s", message)
	return false
}

// StreamResponsesToOpenAI converts an OpenAI Responses SSE stream into OpenAI
// chat.completion.chunk SSE frames.
func StreamResponsesToOpenAI(src io.Reader, dst io.Writer, modelName string) error {
	sink := newResponsesStreamSink(dst, modelName)
	err := emitResponsesAsOpenAI(src, sink)
	if sink.err != nil {
		return sink.err
	}
	if err != nil {
		return err
	}

	sink.sse.done()
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

	return StreamOpenAIToAnthropicSSE(pr, dst, modelName)
}
