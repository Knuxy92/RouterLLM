package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"routerllm/internal/model"
	"routerllm/internal/util"
)

func TranslateGoogleRequest(body map[string]any, modelName string) ([]byte, string, error) {
	return TranslateGoogleRequestWithResolver(body, modelName, nil)
}

// TranslateGoogleRequestWithResolver converts an OpenAI chat body into a
// Gemini generateContent payload. The upstream is always called on the
// :streamGenerateContent endpoint because the proxy forces streaming.
func TranslateGoogleRequestWithResolver(body map[string]any, modelName string, resolve MediaResolver) ([]byte, string, error) {
	req := make(map[string]any)

	var systemTexts []string
	var contents []any
	toolNames := make(map[string]string)

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
			part, err := googleToolResponsePart(msg, toolNames)
			if err != nil {
				return nil, "", fmt.Errorf("tool message: %w", err)
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []any{part}})
			continue
		}

		parts, err := googlePartsFromContent(msg["content"], resolve)
		if err != nil {
			return nil, "", fmt.Errorf("message content: %w", err)
		}
		if role == "assistant" {
			callParts, err := googleFunctionCallParts(msg["tool_calls"], toolNames)
			if err != nil {
				return nil, "", fmt.Errorf("tool_calls: %w", err)
			}
			parts = append(parts, callParts...)
		}
		if len(parts) == 0 {
			continue
		}

		gRole := "user"
		if role == "assistant" {
			gRole = "model"
		}
		contents = append(contents, map[string]any{"role": gRole, "parts": parts})
	}

	if len(systemTexts) > 0 {
		req["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": strings.Join(systemTexts, "\n\n")}}}
	}
	if len(contents) == 0 {
		return nil, "", fmt.Errorf("no translatable messages in request")
	}
	req["contents"] = contents

	if tools, ok := body["tools"].([]any); ok {
		if decls := googleFunctionDeclarations(tools); len(decls) > 0 {
			req["tools"] = []any{map[string]any{"functionDeclarations": decls}}
		}
	}
	if tc, ok := body["tool_choice"]; ok {
		if cfg := googleToolConfig(tc); cfg != nil {
			req["toolConfig"] = cfg
		}
	}

	gen := map[string]any{}
	for _, key := range maxTokenKeys {
		if v, ok := body[key]; ok {
			gen["maxOutputTokens"] = intValue(v, 0)
		}
	}
	for _, key := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty"} {
		if v, ok := body[key]; ok {
			gen[key] = v
		}
	}
	if stop := googleStopSequences(body["stop"]); len(stop) > 0 {
		gen["stopSequences"] = stop
	}
	if rf, ok := body["response_format"].(map[string]any); ok {
		applyGoogleResponseFormat(rf, gen)
	}
	applyGoogleThinking(body, gen)
	if len(gen) > 0 {
		req["generationConfig"] = gen
	}

	data, err := json.Marshal(req)
	path := "/v1beta/models/" + url.PathEscape(modelName) + ":streamGenerateContent?alt=sse"
	return data, path, err
}

func googlePartsFromContent(content any, resolve MediaResolver) ([]any, error) {
	if content == nil {
		return nil, nil
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return nil, nil
		}
		return []any{map[string]any{"text": s}}, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, fmt.Errorf("content must be a string or array")
	}

	var out []any
	for _, p := range parts {
		block, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content part must be an object")
		}
		typeName, _ := block["type"].(string)
		switch typeName {
		case "text", "input_text":
			if t, _ := block["text"].(string); t != "" {
				out = append(out, map[string]any{"text": t})
			}
		case "image_url":
			image, ok := block["image_url"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("image_url must be an object")
			}
			ref, _ := image["url"].(string)
			part, err := googleMediaPart(ref, resolve)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "input_image":
			ref, _ := block["image_url"].(string)
			part, err := googleMediaPart(ref, resolve)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		case "image_file", "input_file", "file":
			ref := firstString(block, "file_id", "file_url", "file_data", "url")
			part, err := googleMediaPart(ref, resolve)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		default:
			return nil, fmt.Errorf("unsupported content part type %q", typeName)
		}
	}
	return out, nil
}

func googleMediaPart(reference string, resolve MediaResolver) (map[string]any, error) {
	data, mediaType, err := decodeMediaReference(reference)
	if err != nil && resolve != nil {
		data, mediaType, err = resolve(reference)
	}
	if err != nil {
		return nil, err
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	return map[string]any{"inline_data": map[string]any{
		"mime_type": mediaType,
		"data":      base64.StdEncoding.EncodeToString(data),
	}}, nil
}

func googleFunctionCallParts(raw any, toolNames map[string]string) ([]any, error) {
	tcs, ok := raw.([]any)
	if !ok {
		return nil, nil
	}

	var parts []any
	for _, tc := range tcs {
		tcm, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		id, _ := tcm["id"].(string)
		fn, _ := tcm["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if id != "" && name != "" {
			toolNames[id] = name
		}

		var args any = map[string]any{}
		if argsRaw, ok := fn["arguments"].(string); ok && strings.TrimSpace(argsRaw) != "" {
			if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
				return nil, fmt.Errorf("tool call %q arguments: %w", name, err)
			}
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": name, "args": args}})
	}
	return parts, nil
}

func googleToolResponsePart(msg map[string]any, toolNames map[string]string) (map[string]any, error) {
	toolCallID, _ := msg["tool_call_id"].(string)
	name := toolNames[toolCallID]
	if name == "" {
		name = toolCallID
	}
	if name == "" {
		return nil, fmt.Errorf("tool message without tool_call_id")
	}

	var result any = systemText(msg["content"])
	if s, ok := result.(string); ok && strings.TrimSpace(s) != "" {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			result = parsed
		}
	}

	return map[string]any{"functionResponse": map[string]any{
		"name":     name,
		"response": map[string]any{"result": result},
	}}, nil
}

var googleSchemaKeys = map[string]bool{
	"type": true, "format": true, "description": true, "nullable": true,
	"enum": true, "items": true, "properties": true, "required": true,
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true, "pattern": true, "title": true,
	"example": true, "anyOf": true, "propertyOrdering": true,
}

// sanitizeGoogleSchema strips JSON-Schema keywords the Gemini API rejects and
// normalizes type casing / null-unions to its OpenAPI-style subset.
func sanitizeGoogleSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		switch key {
		case "type":
			switch t := value.(type) {
			case string:
				out["type"] = strings.ToLower(t)
			case []any:
				var nonNull any
				nullable := false
				for _, entry := range t {
					if s, ok := entry.(string); ok {
						if s == "null" {
							nullable = true
						} else if nonNull == nil {
							nonNull = strings.ToLower(s)
						}
					}
				}
				if nonNull != nil {
					out["type"] = nonNull
				}
				if nullable {
					out["nullable"] = true
				}
			}
		case "items":
			if items, ok := value.(map[string]any); ok {
				out["items"] = sanitizeGoogleSchema(items)
			}
		case "properties":
			if props, ok := value.(map[string]any); ok {
				cleaned := make(map[string]any, len(props))
				for name, prop := range props {
					if propMap, ok := prop.(map[string]any); ok {
						cleaned[name] = sanitizeGoogleSchema(propMap)
					}
				}
				out["properties"] = cleaned
			}
		case "anyOf":
			if variants, ok := value.([]any); ok {
				cleaned := make([]any, 0, len(variants))
				for _, variant := range variants {
					if vm, ok := variant.(map[string]any); ok {
						cleaned = append(cleaned, sanitizeGoogleSchema(vm))
					}
				}
				out["anyOf"] = cleaned
			}
		default:
			if googleSchemaKeys[key] {
				out[key] = value
			}
		}
	}
	return out
}

func googleFunctionDeclarations(tools []any) []any {
	var decls []any
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if typeName, _ := tm["type"].(string); typeName != "" && typeName != "function" {
			continue
		}
		fn, _ := tm["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}
		decl := map[string]any{"name": name}
		if d, ok := fn["description"].(string); ok && d != "" {
			decl["description"] = d
		}
		if params, ok := fn["parameters"].(map[string]any); ok {
			if s := sanitizeGoogleSchema(params); len(s) > 0 {
				decl["parameters"] = s
			}
		}
		decls = append(decls, decl)
	}
	return decls
}

func googleToolConfig(choice any) map[string]any {
	mode := ""
	var allowed []string
	switch tc := choice.(type) {
	case string:
		switch tc {
		case "none":
			mode = "NONE"
		case "required", "any":
			mode = "ANY"
		}
	case map[string]any:
		if fn, ok := tc["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				mode = "ANY"
				allowed = []string{name}
			}
		}
	}
	if mode == "" {
		return nil
	}

	cfg := map[string]any{"mode": mode}
	if len(allowed) > 0 {
		cfg["allowed_function_names"] = allowed
	}
	return map[string]any{"functionCallingConfig": cfg}
}

func googleStopSequences(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		var out []string
		for _, entry := range v {
			if s, ok := entry.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func applyGoogleResponseFormat(rf map[string]any, gen map[string]any) {
	switch rf["type"] {
	case "json_object":
		gen["responseMimeType"] = "application/json"
	case "json_schema":
		gen["responseMimeType"] = "application/json"
		if wrapper, ok := rf["json_schema"].(map[string]any); ok {
			if schema, ok := wrapper["schema"].(map[string]any); ok {
				if s := sanitizeGoogleSchema(schema); len(s) > 0 {
					gen["responseSchema"] = s
				}
			}
		}
	}
}

// applyGoogleThinking maps the canonical reasoning keys (reasoning_effort,
// thinking_budget) onto the Gemini thinkingConfig shape.
func applyGoogleThinking(body map[string]any, gen map[string]any) {
	thinking := make(map[string]any)

	effort, _ := body["reasoning_effort"].(string)
	budget := intValue(body["thinking_budget"], 0)

	switch {
	case budget > 0:
		thinking["thinkingBudget"] = budget
	case effort == "none":
		thinking["thinkingBudget"] = 0
	case effort == "low" || effort == "medium" || effort == "high":
		thinking["thinkingLevel"] = effort
	case effort == "xhigh" || effort == "max":
		thinking["thinkingLevel"] = "high"
	}

	if len(thinking) > 0 {
		gen["thinkingConfig"] = thinking
	}
}

type googleChunk struct {
	ResponseID string `json:"responseId"`
	Candidates []struct {
		Content *struct {
			Parts []struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (c *googleChunk) usageJSON() json.RawMessage {
	if c.UsageMetadata == nil {
		return nil
	}
	u := c.UsageMetadata
	return openAIUsageJSON(u.PromptTokenCount, u.CandidatesTokenCount+u.ThoughtsTokenCount, u.TotalTokenCount)
}

func mapGoogleFinishReason(reason string) string {
	switch reason {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION":
		return "content_filter"
	default:
		return "stop"
	}
}

// googleSink receives the OpenAI-shaped output of the shared Gemini event
// core.
type googleSink interface {
	// upstreamError handles an error object embedded in a Gemini chunk. It
	// reports whether the rest of the chunk is dropped and whether iteration
	// should stop.
	upstreamError(message string) (skip, stop bool)
	responseID(id string)
	usage(raw json.RawMessage)
	// blocked handles a promptFeedback block on a candidate-less chunk and
	// reports whether iteration should stop.
	blocked() bool
	candidate()
	delta(delta model.Delta)
	// finish handles a candidate finish_reason and reports whether iteration
	// should stop.
	finish(reason string) bool
}

type googleBufferSink struct {
	msgID        string
	content      strings.Builder
	reasoning    strings.Builder
	toolCalls    []model.ToolCall
	finishReason string
	lastUsage    json.RawMessage
}

func (s *googleBufferSink) upstreamError(string) (skip, stop bool) { return true, false }

func (s *googleBufferSink) responseID(id string) {
	if s.msgID == "" {
		s.msgID = id
	}
}

func (s *googleBufferSink) usage(raw json.RawMessage) { s.lastUsage = raw }

func (s *googleBufferSink) blocked() bool {
	s.finishReason = "content_filter"
	return false
}

func (s *googleBufferSink) candidate() {}

func (s *googleBufferSink) delta(delta model.Delta) {
	s.content.WriteString(delta.Content)
	s.reasoning.WriteString(delta.ReasoningContent)
	s.toolCalls = append(s.toolCalls, delta.ToolCalls...)
}

func (s *googleBufferSink) finish(reason string) bool {
	s.finishReason = reason
	return false
}

func (s *googleBufferSink) document(modelName string) ([]byte, error) {
	finish := s.finishReason
	if finish == "" {
		finish = "stop"
	}

	msg := model.Message{Role: "assistant", Content: s.content.String(), ToolCalls: s.toolCalls}
	if s.reasoning.Len() > 0 {
		msg.ReasoningContent = s.reasoning.String()
	}

	result := model.ChatCompletionResponse{
		ID:      s.msgID,
		Object:  model.ChatCompletionObject,
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []model.Choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   s.lastUsage,
	}
	return json.Marshal(result)
}

// emitGoogleAsOpenAI translates a Gemini SSE stream into OpenAI deltas and
// hands them to the sink.
func emitGoogleAsOpenAI(src io.Reader, sink googleSink) error {
	toolIdx := 0

	_, err := util.IterDataLines(src, func(payload string) bool {
		var chunk googleChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return true
		}

		if chunk.Error != nil {
			skip, stop := sink.upstreamError(chunk.Error.Message)
			if stop {
				return false
			}
			if skip {
				return true
			}
		}

		sink.responseID(chunk.ResponseID)
		if chunk.UsageMetadata != nil {
			sink.usage(chunk.usageJSON())
		}

		if len(chunk.Candidates) == 0 {
			if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
				return sink.blocked()
			}
			return true
		}

		sink.candidate()
		cand := chunk.Candidates[0]
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					args := string(part.FunctionCall.Args)
					if strings.TrimSpace(args) == "" || string(args) == "null" {
						args = "{}"
					}
					sink.delta(model.Delta{ToolCalls: []model.ToolCall{{
						Index:    toolIdx,
						ID:       fmt.Sprintf("call_%d", toolIdx),
						Type:     "function",
						Function: model.ToolCallFunction{Name: part.FunctionCall.Name, Arguments: args},
					}}})
					toolIdx++
				case part.Thought:
					if part.Text != "" {
						sink.delta(model.Delta{ReasoningContent: part.Text})
					}
				default:
					if part.Text != "" {
						sink.delta(model.Delta{Content: part.Text})
					}
				}
			}
		}

		if cand.FinishReason != "" {
			return sink.finish(mapGoogleFinishReason(cand.FinishReason))
		}
		return true
	})
	return err
}

// BufferGoogleToOpenAI reads a Gemini SSE stream and emits a single OpenAI
// chat.completion JSON document.
func BufferGoogleToOpenAI(src io.Reader, modelName string) ([]byte, error) {
	sink := &googleBufferSink{}
	if err := emitGoogleAsOpenAI(src, sink); err != nil {
		return nil, err
	}
	return sink.document(modelName)
}

type googleStreamSink struct {
	sse       sseWriter
	msgID     string
	idSet     bool
	sentRole  bool
	created   int64
	model     string
	lastUsage json.RawMessage
}

func newGoogleStreamSink(dst io.Writer, modelName string) *googleStreamSink {
	return &googleStreamSink{
		sse:     newSSEWriter(dst),
		created: time.Now().Unix(),
		model:   modelName,
	}
}

func (s *googleStreamSink) write(delta model.Delta, finish *string) {
	s.sse.frame(model.StreamChunk{
		ID:      s.msgID,
		Object:  model.ChatCompletionChunkObject,
		Created: s.created,
		Model:   s.model,
		Choices: []model.StreamChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	})
}

func (s *googleStreamSink) upstreamError(message string) (skip, stop bool) {
	if message == "" {
		return false, false
	}

	s.sse.frame(map[string]any{"error": map[string]any{
		"message": message, "code": "upstream_error", "type": "server_error",
	}})
	return true, true
}

func (s *googleStreamSink) responseID(id string) {
	if s.idSet {
		return
	}
	s.idSet = true
	if id != "" {
		s.msgID = id
		return
	}

	s.msgID = chatCompletionIDPrefix + fmt.Sprintf("google-%d", s.created)
}

func (s *googleStreamSink) usage(raw json.RawMessage) { s.lastUsage = raw }

func (s *googleStreamSink) blocked() bool {
	fr := "content_filter"
	s.write(model.Delta{}, &fr)
	return true
}

func (s *googleStreamSink) candidate() {
	if s.sentRole {
		return
	}
	s.sentRole = true
	s.write(model.Delta{Role: "assistant"}, nil)
}

func (s *googleStreamSink) delta(delta model.Delta) {
	s.write(delta, nil)
}

func (s *googleStreamSink) finish(reason string) bool {
	s.write(model.Delta{}, &reason)
	return true
}

func (s *googleStreamSink) close() {
	if s.lastUsage != nil {
		s.sse.frame(model.StreamChunk{
			ID: s.msgID, Object: model.ChatCompletionChunkObject, Created: s.created, Model: s.model,
			Choices: []model.StreamChoice{{Index: 0, Delta: model.Delta{}}},
			Usage:   s.lastUsage,
		})
	}
	s.sse.done()
}

// StreamGoogleToOpenAI converts a Gemini SSE stream into OpenAI
// chat.completion.chunk SSE frames.
func StreamGoogleToOpenAI(src io.Reader, dst io.Writer, modelName string) error {
	sink := newGoogleStreamSink(dst, modelName)
	err := emitGoogleAsOpenAI(src, sink)
	sink.close()
	return err
}

// StreamGoogleToAnthropicSSE converts a Gemini SSE stream into Anthropic SSE
// by routing it through the OpenAI chunk shape and the existing converter.
func StreamGoogleToAnthropicSSE(src io.Reader, dst http.ResponseWriter, modelName string) error {
	pr, pw := io.Pipe()
	go func() {
		err := StreamGoogleToOpenAI(src, pw, modelName)
		pw.CloseWithError(err)
	}()

	defer pr.Close()
	return StreamOpenAIToAnthropicSSE(pr, dst, modelName)
}
