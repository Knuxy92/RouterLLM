package adapter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"routerllm/internal/model"
	"routerllm/internal/util"
)

// TranslateCodexRequest converts an OpenAI chat-completions body into the Codex
// Responses API request. The caller has already resolved the upstream model
// name and folded the reasoning dialects into the canonical reasoning_effort
// key; system/developer text moves to instructions, the remaining messages
// become input items.
func TranslateCodexRequest(body map[string]any, modelName string) ([]byte, string, error) {
	req := make(map[string]any)
	req["model"] = modelName

	var instructions []string
	var input []any

	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)
		switch role {
		case "system", "developer":
			if text := codexTextContent(msg["content"]); text != "" {
				instructions = append(instructions, text)
			}

		case "user":
			input = append(input, codexUserItem(msg["content"]))

		case "assistant":
			calls := codexAssistantCalls(msg)
			if text := codexTextContent(msg["content"]); text != "" || len(calls) > 0 {
				input = append(input, map[string]any{"role": "assistant", "content": text})
			}
			input = append(input, calls...)

		case "tool":
			callID, _ := msg["tool_call_id"].(string)
			if callID == "" {
				callID = "unknown"
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  codexTextContent(msg["content"]),
			})

		case "function":
			name, _ := msg["name"].(string)
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": "fc_" + name,
				"output":  codexTextContent(msg["content"]),
			})
		}
	}

	req["instructions"] = strings.Join(instructions, "\n\n")
	if len(input) == 0 {
		input = append(input, map[string]any{"role": "user", "content": ""})
	}
	req["input"] = input
	req["tools"] = codexToolDefs(body)
	req["tool_choice"] = codexToolChoice(body["tool_choice"])
	req["parallel_tool_calls"] = true
	req["stream"] = true
	req["store"] = false
	if reasoning := codexReasoning(body); reasoning != nil {
		req["reasoning"] = reasoning
	}
	if text := codexTextFormat(body["response_format"]); text != nil {
		req["text"] = text
	}

	data, err := json.Marshal(req)
	return data, "/codex/responses", err
}

// codexTextContent joins the text of an OpenAI content field, accepting both a
// plain string and an array of parts.
func codexTextContent(content any) string {
	if text, ok := content.(string); ok {
		return text
	}

	parts, ok := content.([]any)
	if !ok {
		return ""
	}

	var texts []string
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := part["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// codexUserItem builds the input item for a user message: a plain string
// unless an image part is present, in which case it becomes an array of
// input_text / input_image parts (any image detail is discarded).
func codexUserItem(content any) map[string]any {
	if text, ok := content.(string); ok {
		return map[string]any{"role": "user", "content": text}
	}

	parts, ok := content.([]any)
	if !ok {
		return map[string]any{"role": "user", "content": ""}
	}

	hasImage := false
	var out []any
	var texts []string
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		typeName, _ := part["type"].(string)
		switch typeName {
		case "text", "input_text", "output_text":
			text, _ := part["text"].(string)
			texts = append(texts, text)
			out = append(out, map[string]any{"type": "input_text", "text": text})

		case "image_url", "input_image":
			ref := codexImageReference(part)
			if ref == "" {
				continue
			}
			hasImage = true
			out = append(out, map[string]any{"type": "input_image", "image_url": ref})
		}
	}

	if !hasImage {
		return map[string]any{"role": "user", "content": strings.Join(texts, "\n")}
	}
	return map[string]any{"role": "user", "content": out}
}

func codexImageReference(part map[string]any) string {
	if image, ok := part["image_url"].(map[string]any); ok {
		ref, _ := image["url"].(string)
		return ref
	}

	ref, _ := part["image_url"].(string)
	return ref
}

func codexAssistantCalls(msg map[string]any) []any {
	var items []any

	if calls, ok := msg["tool_calls"].([]any); ok {
		for _, raw := range calls {
			call, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if typeName, _ := call["type"].(string); typeName != "" && typeName != "function" {
				continue
			}

			fn, _ := call["function"].(map[string]any)
			id, _ := call["id"].(string)
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)
			items = append(items, map[string]any{
				"type":      "function_call",
				"call_id":   id,
				"name":      name,
				"arguments": args,
			})
		}
	}

	if legacy, ok := msg["function_call"].(map[string]any); ok {
		name, _ := legacy["name"].(string)
		args, _ := legacy["arguments"].(string)
		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   "fc_" + name,
			"name":      name,
			"arguments": args,
		})
	}
	return items
}

func codexToolDefs(body map[string]any) []any {
	defs := []any{}

	if tools, ok := body["tools"].([]any); ok {
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			typeName, _ := tool["type"].(string)
			switch typeName {
			case "function", "":
				if def := codexFunctionTool(tool["function"]); def != nil {
					defs = append(defs, def)
				}
			case "web_search", "web_search_preview":
				defs = append(defs, map[string]any{"type": "web_search"})
			default:
				defs = append(defs, tool)
			}
		}
		return defs
	}

	if functions, ok := body["functions"].([]any); ok {
		for _, raw := range functions {
			if def := codexFunctionTool(raw); def != nil {
				defs = append(defs, def)
			}
		}
	}
	return defs
}

// codexFunctionTool converts an OpenAI function definition into the Codex
// tool shape; strict is always materialized.
func codexFunctionTool(raw any) map[string]any {
	fn, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	name, _ := fn["name"].(string)
	if name == "" {
		return nil
	}

	strict := false
	if s, ok := fn["strict"].(bool); ok {
		strict = s
	}

	def := map[string]any{"type": "function", "name": name, "strict": strict}
	if description, ok := fn["description"].(string); ok && description != "" {
		def["description"] = description
	}
	if parameters, ok := fn["parameters"].(map[string]any); ok {
		def["parameters"] = parameters
	}
	return def
}

func codexToolChoice(raw any) any {
	switch choice := raw.(type) {
	case string:
		switch choice {
		case "none", "auto", "required":
			return choice
		}
	case map[string]any:
		if fn, ok := choice["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]any{"type": "function", "name": name}
			}
		}
	}
	return "auto"
}

// codexReasoning folds the canonical reasoning_effort into the Codex reasoning
// block. The upstream validates the value itself — its 400 enumerates the
// accepted set (none|minimal|low|medium|high|xhigh|max) — so every non-empty
// effort passes through verbatim and only an absent effort drops the block,
// deferring to the model default.
func codexReasoning(body map[string]any) map[string]any {
	effort, _ := body["reasoning_effort"].(string)
	if effort == "" {
		return nil
	}

	return map[string]any{"effort": effort, "summary": "auto"}
}

func codexTextFormat(raw any) map[string]any {
	rf, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	switch rf["type"] {
	case "json_object":
		return map[string]any{"format": map[string]any{"type": "json_object"}}

	case "json_schema":
		wrapper, ok := rf["json_schema"].(map[string]any)
		if !ok {
			return nil
		}

		name, _ := wrapper["name"].(string)
		strict := false
		if s, ok := wrapper["strict"].(bool); ok {
			strict = s
		}

		format := map[string]any{"type": "json_schema", "name": name, "strict": strict}
		if schema, ok := wrapper["schema"].(map[string]any); ok {
			format["schema"] = schema
		}
		return map[string]any{"format": format}
	}
	return nil
}

type codexInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type codexOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type codexUsage struct {
	InputTokens         int                       `json:"input_tokens"`
	OutputTokens        int                       `json:"output_tokens"`
	InputTokensDetails  *codexInputTokensDetails  `json:"input_tokens_details"`
	OutputTokensDetails *codexOutputTokensDetails `json:"output_tokens_details"`
}

func (u *codexUsage) toOpenAI() json.RawMessage {
	if u == nil {
		return nil
	}

	cached, reasoning := 0, 0
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		reasoning = u.OutputTokensDetails.ReasoningTokens
	}

	return json.RawMessage(fmt.Sprintf(
		`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,`+
			`"prompt_tokens_details":{"cached_tokens":%d},`+
			`"completion_tokens_details":{"reasoning_tokens":%d}}`,
		u.InputTokens, u.OutputTokens, u.InputTokens+u.OutputTokens, cached, reasoning,
	))
}

type codexError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type codexItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type codexEvent struct {
	Type      string      `json:"type"`
	Delta     string      `json:"delta"`
	CallID    string      `json:"call_id"`
	ItemID    string      `json:"item_id"`
	Arguments string      `json:"arguments"`
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	Error     *codexError `json:"error"`
	Item      *codexItem  `json:"item"`
	Response  *struct {
		Usage *codexUsage `json:"usage"`
		Error *codexError `json:"error"`
	} `json:"response"`
}

func (e *codexEvent) isFunctionCallItem() bool {
	return e.Item != nil && e.Item.Type == "function_call"
}

// callKey resolves the tool call a delta/done event belongs to: Codex sends the
// item id on argument events and the call id on item events.
func (e *codexEvent) callKey() string {
	if e.CallID != "" {
		return e.CallID
	}
	return e.ItemID
}

func (e *codexEvent) failure() *codexError {
	for _, candidate := range []*codexError{e.Error, e.responseError()} {
		if candidate != nil && (candidate.Message != "" || candidate.Code != "") {
			return candidate
		}
	}

	if e.Message != "" || e.Code != "" {
		return &codexError{Code: e.Code, Message: e.Message}
	}
	return nil
}

func (e *codexEvent) responseError() *codexError {
	if e.Response == nil {
		return nil
	}
	return e.Response.Error
}

// codexSink receives the OpenAI-shaped output of the shared Codex event core.
type codexSink interface {
	prologue()
	delta(model.Delta)
	finish(finishReason string, usage json.RawMessage)
	failure(message, code string)
}

type codexToolCall struct {
	index        int
	id           string
	name         string
	started      bool
	argsStreamed bool
	argsDone     bool
}

// codexToolRegistry maps every id an upstream call is known by (item id and
// call id) onto one tool call record, so a start and its argument events
// resolve to the same OpenAI tool index.
type codexToolRegistry struct {
	calls     map[string]*codexToolCall
	nextIndex int
}

func (r *codexToolRegistry) resolve(ids ...string) *codexToolCall {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if call, ok := r.calls[id]; ok {
			return call
		}
	}

	call := &codexToolCall{index: r.nextIndex}
	r.nextIndex++
	if r.calls == nil {
		r.calls = make(map[string]*codexToolCall)
	}
	for _, id := range ids {
		if id != "" {
			r.calls[id] = call
		}
	}
	return call
}

// emitCodexAsOpenAI is the single parsing core behind all three output paths:
// it translates Codex SSE events into OpenAI deltas and hands them to the sink.
func emitCodexAsOpenAI(src io.Reader, sink codexSink) error {
	var registry codexToolRegistry
	hasToolCalls := false

	sink.prologue()

	startCall := func(call *codexToolCall, id, name string) {
		if call.started {
			return
		}

		call.started = true
		hasToolCalls = true
		if id != "" {
			call.id = id
		}
		if name != "" {
			call.name = name
		}

		sink.delta(model.Delta{ToolCalls: []model.ToolCall{{
			Index:    call.index,
			ID:       call.id,
			Type:     "function",
			Function: model.ToolCallFunction{Name: call.name},
		}}})
	}
	streamArguments := func(call *codexToolCall, delta string) {
		if delta == "" {
			return
		}

		call.argsStreamed = true
		sink.delta(model.Delta{ToolCalls: []model.ToolCall{{
			Index:    call.index,
			Function: model.ToolCallFunction{Arguments: delta},
		}}})
	}
	emitWholeArguments := func(call *codexToolCall, args string) {
		if args == "" || call.argsStreamed || call.argsDone {
			return
		}

		call.argsDone = true
		sink.delta(model.Delta{ToolCalls: []model.ToolCall{{
			Index:    call.index,
			Function: model.ToolCallFunction{Arguments: args},
		}}})
	}

	_, err := util.IterDataLines(src, func(payload string) bool {
		var evt codexEvent
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			return true
		}

		switch evt.Type {
		case "response.reasoning_summary_text.delta":
			if evt.Delta != "" {
				sink.delta(model.Delta{ReasoningContent: evt.Delta})
			}

		case "response.output_text.delta":
			if evt.Delta != "" {
				sink.delta(model.Delta{Content: evt.Delta})
			}

		case "response.output_item.added":
			if !evt.isFunctionCallItem() {
				return true
			}

			call := registry.resolve(evt.Item.ID, evt.Item.CallID)
			if evt.Item.CallID != "" && evt.Item.Name != "" {
				startCall(call, evt.Item.CallID, evt.Item.Name)
			}

		case "response.function_call_arguments.delta":
			if evt.Delta == "" {
				return true
			}

			call := registry.resolve(evt.callKey())
			hasToolCalls = true
			streamArguments(call, evt.Delta)

		case "response.function_call_arguments.done":
			call := registry.resolve(evt.callKey())
			hasToolCalls = true
			emitWholeArguments(call, evt.Arguments)

		case "response.output_item.done":
			if !evt.isFunctionCallItem() {
				return true
			}

			call := registry.resolve(evt.Item.ID, evt.Item.CallID)
			startCall(call, evt.Item.CallID, evt.Item.Name)
			emitWholeArguments(call, evt.Item.Arguments)

		case "response.completed":
			reason := "stop"
			if hasToolCalls {
				reason = "tool_calls"
			}

			var usage json.RawMessage
			if evt.Response != nil {
				usage = evt.Response.Usage.toOpenAI()
			}
			sink.finish(reason, usage)
			return false

		case "error", "response.failed":
			if failure := evt.failure(); failure != nil {
				sink.failure(failure.Message, failure.Code)
			}
			return false
		}
		return true
	})

	return err
}

type codexStreamSink struct {
	dst     io.Writer
	flusher http.Flusher
	id      string
	model   string
	created int64
}

func newCodexStreamSink(dst io.Writer, modelName string) *codexStreamSink {
	flusher, _ := dst.(http.Flusher)
	return &codexStreamSink{
		dst:     dst,
		flusher: flusher,
		id:      newCodexChatID(),
		model:   modelName,
		created: time.Now().Unix(),
	}
}

func (s *codexStreamSink) write(delta model.Delta, finish *string, usage json.RawMessage) {
	data, _ := json.Marshal(model.StreamChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []model.StreamChoice{{Index: 0, Delta: delta, FinishReason: finish}},
		Usage:   usage,
	})
	fmt.Fprintf(s.dst, "data: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *codexStreamSink) prologue() {
	s.write(model.Delta{Role: "assistant"}, nil, nil)
}

func (s *codexStreamSink) delta(delta model.Delta) {
	s.write(delta, nil, nil)
}

func (s *codexStreamSink) finish(finishReason string, usage json.RawMessage) {
	s.write(model.Delta{}, &finishReason, usage)
}

func (s *codexStreamSink) failure(message, code string) {
	data, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "upstream_error",
		"code":    code,
	}})
	fmt.Fprintf(s.dst, "data: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *codexStreamSink) closeStream() {
	fmt.Fprintf(s.dst, "data: [DONE]\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

type codexBufferSink struct {
	content      strings.Builder
	reasoning    strings.Builder
	tools        map[int]*model.ToolCall
	usage        json.RawMessage
	finishReason string
	errCode      string
	errMsg       string
}

func (s *codexBufferSink) prologue() {}

func (s *codexBufferSink) delta(delta model.Delta) {
	s.content.WriteString(delta.Content)
	s.reasoning.WriteString(delta.ReasoningContent)

	for _, tc := range delta.ToolCalls {
		call, ok := s.tools[tc.Index]
		if !ok {
			if s.tools == nil {
				s.tools = make(map[int]*model.ToolCall)
			}
			call = &model.ToolCall{Index: tc.Index}
			s.tools[tc.Index] = call
		}
		if tc.ID != "" {
			call.ID = tc.ID
		}
		if tc.Type != "" {
			call.Type = tc.Type
		}
		if tc.Function.Name != "" {
			call.Function.Name = tc.Function.Name
		}
		call.Function.Arguments += tc.Function.Arguments
	}
}

func (s *codexBufferSink) finish(finishReason string, usage json.RawMessage) {
	s.finishReason = finishReason
	s.usage = usage
}

func (s *codexBufferSink) failure(message, code string) {
	s.errMsg = message
	s.errCode = code
}

// StreamCodexToOpenAI converts a Codex SSE stream into OpenAI
// chat.completion.chunk frames, always terminating with data: [DONE].
func StreamCodexToOpenAI(src io.Reader, dst http.ResponseWriter, modelName string) error {
	return streamCodexToOpenAI(src, dst, modelName)
}

// streamCodexToOpenAI is the io.Writer form used by the Anthropic pipe;
// flusher support is asserted, not required.
func streamCodexToOpenAI(src io.Reader, dst io.Writer, modelName string) error {
	sink := newCodexStreamSink(dst, modelName)
	err := emitCodexAsOpenAI(src, sink)
	sink.closeStream()
	return err
}

// BufferCodexToOpenAI drains a Codex SSE stream into one non-streaming OpenAI
// chat.completion JSON document.
func BufferCodexToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	sink := &codexBufferSink{}
	if err := emitCodexAsOpenAI(src, sink); err != nil {
		return nil, err
	}
	if sink.errMsg != "" {
		if sink.errCode != "" {
			return nil, fmt.Errorf("upstream error %s: %s", sink.errCode, sink.errMsg)
		}
		return nil, fmt.Errorf("upstream error: %s", sink.errMsg)
	}

	indexes := make([]int, 0, len(sink.tools))
	for index := range sink.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	var toolCalls []model.ToolCall
	for _, index := range indexes {
		toolCalls = append(toolCalls, *sink.tools[index])
	}

	msg := model.Message{Role: "assistant", Content: sink.content.String(), ToolCalls: toolCalls}
	if reasoning := sink.reasoning.String(); reasoning != "" {
		msg.ReasoningContent = reasoning
	}

	finish := sink.finishReason
	if finish == "" {
		finish = "stop"
	}

	result := model.ChatCompletionResponse{
		ID:      newCodexChatID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []model.Choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   sink.usage,
	}
	return json.Marshal(result)
}

// StreamCodexToAnthropicSSE converts a Codex SSE stream into Anthropic SSE by
// routing it through the OpenAI chunk shape and the existing converter.
func StreamCodexToAnthropicSSE(src io.Reader, dst http.ResponseWriter, modelName string) {
	pr, pw := io.Pipe()
	go func() {
		err := streamCodexToOpenAI(src, pw, modelName)
		pw.CloseWithError(err)
	}()

	defer pr.Close()
	_ = streamOpenAIToAnthropicSSE(pr, dst, modelName)
}

func newCodexChatID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("chatcmpl-%024x", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(buf[:])
}
