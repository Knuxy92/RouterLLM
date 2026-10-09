package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"routerllm/internal/model"
	"routerllm/internal/util"
)

func TranslateRequest(body map[string]any, modelName string) ([]byte, string, error) {
	return TranslateRequestWithResolver(body, modelName, nil)
}

func TranslateRequestWithResolver(body map[string]any, modelName string, resolve MediaResolver) ([]byte, string, error) {
	req := make(map[string]any)
	req["model"] = modelName

	var systemParts []string
	var messages []any
	var pendingResults []any

	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}
		messages = append(messages, map[string]any{"role": "user", "content": pendingResults})
		pendingResults = nil
	}

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
			// Tool results arrive as their own "tool" turns; Anthropic wants them
			// as one user turn holding every result block.
			if role == "tool" {
				pendingResults = append(pendingResults, toolResultBlock(msg))
				continue
			}

			flushResults()

			blocks, err := translateOpenAIContent(msg["content"], resolve)
			if err != nil {
				return nil, "", fmt.Errorf("message content: %w", err)
			}
			if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
				blocks = append(blocks, toolUseBlocks(calls)...)
				// Anthropic rejects unknown message fields.
				delete(msg, "tool_calls")
			}
			blocks = dropEmptyTextBlocks(blocks)

			msg["content"] = blocks
			messages = append(messages, msg)
		}
	}
	flushResults()

	if len(systemParts) > 0 {
		req["system"] = strings.Join(systemParts, "\n\n")
	}
	req["messages"] = messages

	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		converted := make([]any, 0, len(tools))
		for _, entry := range tools {
			tool, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if convertedTool, ok := ChatToolToAnthropicTool(tool); ok {
				converted = append(converted, convertedTool)
			}
		}
		if len(converted) > 0 {
			req["tools"] = converted
		}
	}
	if choice, ok := anthropicToolChoice(body["tool_choice"]); ok {
		req["tool_choice"] = choice
	}

	maxTokens := defaultAnthropicMaxTokens
	explicitMaxTokens := false
	for _, key := range maxTokenKeys {
		if mt, ok := body[key]; ok {
			maxTokens = intValue(mt, maxTokens)
			explicitMaxTokens = true
			break
		}
	}
	effort, _ := body["reasoning_effort"].(string)
	budget := intValue(body["thinking_budget"], 0)
	if effort != "none" && (effort != "" || budget > 0) {
		thinking := map[string]any{"type": "enabled"}
		if budget > 0 {
			if maxTokens <= budget {
				if explicitMaxTokens {
					budget = maxTokens - 1
				} else {
					maxTokens = budget + thinkingHeadroom
				}
			}
			thinking["budget_tokens"] = budget
		}
		req["thinking"] = thinking
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

// maxTokenKeys lists the OpenAI token-cap fields accepted on inbound bodies;
// the first present key wins.
var maxTokenKeys = []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}

// ChatToolToAnthropicTool converts one chat-shaped function tool to the
// Anthropic definition shape ({name, description, input_schema}).
func ChatToolToAnthropicTool(entry map[string]any) (map[string]any, bool) {
	fn, ok := entry["function"].(map[string]any)
	if !ok {
		return nil, false
	}

	name, _ := fn["name"].(string)
	if name == "" {
		return nil, false
	}

	tool := map[string]any{"name": name}
	if d, ok := fn["description"].(string); ok {
		tool["description"] = d
	}
	if p, ok := fn["parameters"]; ok && p != nil {
		tool["input_schema"] = p
	} else {
		tool["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	return tool, true
}

// anthropicToolChoice maps the chat tool_choice values onto the Anthropic
// object form. Anything it cannot place is dropped rather than guessed at.
func anthropicToolChoice(choice any) (map[string]any, bool) {
	switch value := choice.(type) {
	case string:
		switch value {
		case "auto":
			return map[string]any{"type": "auto"}, true
		case "required", "any":
			return map[string]any{"type": "any"}, true
		case "none":
			return map[string]any{"type": "none"}, true
		}
	case map[string]any:
		fn, _ := value["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			return map[string]any{"type": "tool", "name": name}, true
		}
	}

	return nil, false
}

// toolUseBlocks turns an assistant turn's tool_calls into the tool_use content
// blocks Anthropic expects, parsing the JSON arguments into an object.
func toolUseBlocks(calls []any) []any {
	blocks := make([]any, 0, len(calls))

	for _, entry := range calls {
		call, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		fn, _ := call["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}

		block := map[string]any{"type": "tool_use", "name": name, "input": map[string]any{}}
		if id, _ := call["id"].(string); id != "" {
			block["id"] = id
		}
		if args, _ := fn["arguments"].(string); args != "" {
			var parsed any
			if err := json.Unmarshal([]byte(args), &parsed); err == nil && parsed != nil {
				block["input"] = parsed
			}
		}
		blocks = append(blocks, block)
	}

	return blocks
}

// toolResultBlock turns one role:"tool" message into a tool_result block.
func toolResultBlock(msg map[string]any) map[string]any {
	block := map[string]any{"type": "tool_result"}
	if id, _ := msg["tool_call_id"].(string); id != "" {
		block["tool_use_id"] = id
	}
	if content, ok := msg["content"]; ok && content != nil {
		block["content"] = content
	} else {
		block["content"] = ""
	}

	return block
}

// dropEmptyTextBlocks removes text blocks with no text so a tool-only assistant
// turn does not carry a blank block into the Anthropic payload.
func dropEmptyTextBlocks(blocks []any) []any {
	kept := make([]any, 0, len(blocks))

	for _, block := range blocks {
		if bm, ok := block.(map[string]any); ok {
			if kind, _ := bm["type"].(string); kind == "text" {
				if text, _ := bm["text"].(string); text == "" {
					continue
				}
			}
		}
		kept = append(kept, block)
	}

	return kept
}

const (
	defaultAnthropicMaxTokens = 4096
	thinkingHeadroom          = 1024
)

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

// contentText flattens an OpenAI-style content field into text: a plain
// string is returned verbatim, array parts contribute their "text" member,
// optionally filtered to one part type and with empty texts dropped.
func contentText(content any, sep, partType string, skipEmpty bool) string {
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
		if partType != "" {
			if t, _ := part["type"].(string); t != partType {
				continue
			}
		}
		text, ok := part["text"].(string)
		if !ok || (skipEmpty && text == "") {
			continue
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, sep)
}

func systemText(content any) string {
	return contentText(content, "\n", "text", true)
}

// openAIUsageJSON renders the OpenAI usage object; the optional extra member
// snippet is spliced in before the closing brace.
func openAIUsageJSON(prompt, completion, total int, extra ...string) json.RawMessage {
	more := ""
	if len(extra) > 0 {
		more = "," + extra[0]
	}
	return json.RawMessage(fmt.Sprintf(
		`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d%s}`,
		prompt, completion, total, more,
	))
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage"`
}

type anthropicToolBlock struct {
	id   string
	name string
	args strings.Builder
}

func (b *anthropicToolBlock) arguments() string {
	args := b.args.String()
	if strings.TrimSpace(args) == "" {
		return "{}"
	}
	return args
}

// anthropicSink receives the OpenAI-shaped output of the shared Anthropic
// event core.
type anthropicSink interface {
	messageMeta(id, model string)
	messageStart()
	toolStart(index int, id, name string)
	textDelta(text string)
	thinkingDelta(text string)
	argsDelta(index int, partialJSON string)
	messageDelta(stopReason string, usage *anthropicUsage)
}

type anthropicBufferSink struct {
	msgID      string
	upModel    string
	content    strings.Builder
	reasoning  strings.Builder
	stopReason string
	usage      *anthropicUsage
	toolBlocks map[int]*anthropicToolBlock
	toolOrder  []int
}

func (s *anthropicBufferSink) messageMeta(id, model string) {
	s.msgID = id
	s.upModel = model
}

func (s *anthropicBufferSink) messageStart() {}

func (s *anthropicBufferSink) toolStart(index int, id, name string) {
	if _, exists := s.toolBlocks[index]; !exists {
		s.toolOrder = append(s.toolOrder, index)
	}
	s.toolBlocks[index] = &anthropicToolBlock{id: id, name: name}
}

func (s *anthropicBufferSink) textDelta(text string) {
	s.content.WriteString(text)
}

func (s *anthropicBufferSink) thinkingDelta(text string) {
	s.reasoning.WriteString(text)
}

func (s *anthropicBufferSink) argsDelta(index int, partialJSON string) {
	block := s.toolBlocks[index]
	if block == nil {
		block = &anthropicToolBlock{}
		s.toolBlocks[index] = block
		s.toolOrder = append(s.toolOrder, index)
	}
	block.args.WriteString(partialJSON)
}

func (s *anthropicBufferSink) messageDelta(stopReason string, usage *anthropicUsage) {
	if stopReason != "" {
		s.stopReason = stopReason
	}
	if usage != nil {
		s.usage = usage
	}
}

func (s *anthropicBufferSink) document() ([]byte, error) {
	sort.Ints(s.toolOrder)
	var toolCalls []model.ToolCall
	for _, blockIndex := range s.toolOrder {
		block := s.toolBlocks[blockIndex]
		toolCalls = append(toolCalls, model.ToolCall{
			Index: len(toolCalls),
			ID:    block.id,
			Type:  "function",
			Function: model.ToolCallFunction{
				Name:      block.name,
				Arguments: block.arguments(),
			},
		})
	}

	msg := model.Message{Role: "assistant", Content: s.content.String(), ToolCalls: toolCalls}
	if reasoning := s.reasoning.String(); reasoning != "" {
		msg.ReasoningContent = reasoning
	}

	result := model.ChatCompletionResponse{
		ID:      s.msgID,
		Object:  model.ChatCompletionObject,
		Created: time.Now().Unix(),
		Model:   s.upModel,
		Choices: []model.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: mapStopReason(s.stopReason),
		}},
	}
	if s.usage != nil {
		result.Usage = openAIUsageJSON(s.usage.InputTokens, s.usage.OutputTokens, s.usage.InputTokens+s.usage.OutputTokens)
	}
	return json.Marshal(result)
}

// emitAnthropicAsOpenAI translates an Anthropic SSE stream into OpenAI deltas
// and hands them to the sink.
func emitAnthropicAsOpenAI(src io.Reader, sink anthropicSink) error {
	_, err := util.IterDataLines(src, func(payload string) bool {
		var event anthropicEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return true
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil {
				sink.messageMeta(event.Message.ID, event.Message.Model)
			}
			sink.messageStart()

		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				sink.toolStart(event.Index, event.ContentBlock.ID, event.ContentBlock.Name)
			}

		case "content_block_delta":
			if event.Delta == nil {
				return true
			}
			switch event.Delta.Type {
			case "text_delta":
				sink.textDelta(event.Delta.Text)
			case "thinking_delta":
				sink.thinkingDelta(event.Delta.Thinking)
			case "input_json_delta":
				sink.argsDelta(event.Index, event.Delta.PartialJSON)
			}

		case "message_delta":
			var stopReason string
			if event.Delta != nil {
				stopReason = event.Delta.StopReason
			}
			sink.messageDelta(stopReason, event.Usage)

		case "message_stop":
			return false
		}
		return true
	})
	return err
}

// BufferAnthropicToOpenAI drains an Anthropic SSE stream into one
// non-streaming OpenAI chat.completion JSON document.
func BufferAnthropicToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	sink := &anthropicBufferSink{toolBlocks: make(map[int]*anthropicToolBlock)}
	if err := emitAnthropicAsOpenAI(src, sink); err != nil {
		return nil, err
	}
	return sink.document()
}

type anthropicStreamSink struct {
	sse           sseWriter
	msgID         string
	created       int64
	model         string
	toolIndexes   map[int]int
	nextToolIndex int
}

func newAnthropicStreamSink(dst http.ResponseWriter, modelName string) *anthropicStreamSink {
	return &anthropicStreamSink{
		sse:         newSSEWriter(dst),
		created:     time.Now().Unix(),
		model:       modelName,
		toolIndexes: make(map[int]int),
	}
}

func (s *anthropicStreamSink) writeChunk(chunk model.StreamChunk) {
	s.sse.frame(chunk)
}

func (s *anthropicStreamSink) writeDelta(delta model.Delta, finish *string) {
	s.writeChunk(model.StreamChunk{
		ID:      s.msgID,
		Object:  model.ChatCompletionChunkObject,
		Created: s.created,
		Model:   s.model,
		Choices: []model.StreamChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	})
}

func (s *anthropicStreamSink) toolIndexFor(blockIndex int) int {
	if idx, ok := s.toolIndexes[blockIndex]; ok {
		return idx
	}

	idx := s.nextToolIndex
	s.nextToolIndex++
	s.toolIndexes[blockIndex] = idx
	return idx
}

func (s *anthropicStreamSink) messageMeta(id, _ string) {
	s.msgID = id
}

func (s *anthropicStreamSink) messageStart() {
	s.writeDelta(model.Delta{Role: "assistant"}, nil)
}

func (s *anthropicStreamSink) toolStart(index int, id, name string) {
	s.writeDelta(model.Delta{ToolCalls: []model.ToolCall{{
		Index: s.toolIndexFor(index),
		ID:    id,
		Type:  "function",
		Function: model.ToolCallFunction{
			Name: name,
		},
	}}}, nil)
}

func (s *anthropicStreamSink) textDelta(text string) {
	if text == "" {
		return
	}
	s.writeDelta(model.Delta{Content: text}, nil)
}

func (s *anthropicStreamSink) thinkingDelta(text string) {
	if text == "" {
		return
	}
	s.writeDelta(model.Delta{ReasoningContent: text}, nil)
}

func (s *anthropicStreamSink) argsDelta(index int, partialJSON string) {
	if partialJSON == "" {
		return
	}
	s.writeDelta(model.Delta{ToolCalls: []model.ToolCall{{
		Index:    s.toolIndexFor(index),
		Function: model.ToolCallFunction{Arguments: partialJSON},
	}}}, nil)
}

func (s *anthropicStreamSink) messageDelta(stopReason string, usage *anthropicUsage) {
	if stopReason == "" {
		return
	}

	fr := mapStopReason(stopReason)
	var usageRaw json.RawMessage
	if usage != nil {
		usageRaw = openAIUsageJSON(usage.InputTokens, usage.OutputTokens, usage.InputTokens+usage.OutputTokens)
	}
	s.writeChunk(model.StreamChunk{
		ID:      s.msgID,
		Object:  model.ChatCompletionChunkObject,
		Created: s.created,
		Model:   s.model,
		Choices: []model.StreamChoice{{
			Index:        0,
			Delta:        model.Delta{},
			FinishReason: &fr,
		}},
		Usage: usageRaw,
	})
}

// StreamAnthropicToOpenAI converts an Anthropic SSE stream into OpenAI
// chat.completion.chunk SSE frames.
func StreamAnthropicToOpenAI(src io.Reader, dst http.ResponseWriter, modelName string) error {
	sink := newAnthropicStreamSink(dst, modelName)
	err := emitAnthropicAsOpenAI(src, sink)
	sink.sse.done()
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
func StreamOpenAIToAnthropicSSE(src io.Reader, dst http.ResponseWriter, modelName string) error {
	sse := newSSEWriter(dst)
	var st anthropicSSEState

	writeSSE := sse.event
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
	emitToolBlockStart := func(ts *toolStreamState) {
		writeSSE("content_block_start", map[string]any{
			"type": "content_block_start", "index": ts.blockIndex,
			"content_block": map[string]any{
				"type": "tool_use",
				"id":   ts.id,
				"name": ts.name,
			},
		})
	}
	emitToolArgsDelta := func(ts *toolStreamState, part string) {
		writeSSE("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": ts.blockIndex,
			"delta": map[string]string{"type": "input_json_delta", "partial_json": part},
		})
	}

	_, err := util.IterDataLines(src, func(payload string) bool {
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
					emitToolBlockStart(ts)
				}

				if ts.started {
					if part := ts.freshArgs(); part != "" {
						emitToolArgsDelta(ts, part)
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
					emitToolBlockStart(ts)
					if part := ts.freshArgs(); part != "" {
						emitToolArgsDelta(ts, part)
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

	return err
}
