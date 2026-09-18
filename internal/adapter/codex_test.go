package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"routerllm/internal/model"
)

func codexFixture() string {
	return strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"think"}`,
		``,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"delta":"Hel"}`,
		``,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"delta":"lo"}`,
		``,
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"city\":"}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"\"SF\"}"}`,
		``,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":2,"arguments":"{\"city\":\"SF\"}"}`,
		``,
		`data: {"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":3}}}}`,
		``,
	}, "\n")
}

func parseCodexChunks(t *testing.T, body string) []model.StreamChunk {
	t.Helper()

	var chunks []model.StreamChunk
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}

		var chunk model.StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("unmarshal chunk %q: %v", payload, err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func decodeCodexRequest(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	data, _, err := TranslateCodexRequest(body, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("TranslateCodexRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return req
}

func TestTranslateCodexRequest(t *testing.T) {
	body := map[string]any{
		"model": "ignored",
		"messages": []any{
			map[string]any{"role": "system", "content": "be terse"},
			map[string]any{"role": "developer", "content": []any{map[string]any{"type": "text", "text": "dev note"}}},
			map[string]any{"role": "user", "content": "hello"},
			map[string]any{"role": "assistant", "content": "hi", "tool_calls": []any{map[string]any{
				"id":       "call_1",
				"type":     "function",
				"function": map[string]any{"name": "get_weather", "arguments": `{"city":"SF"}`},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "sunny"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "look up weather",
					"parameters":  map[string]any{"type": "object"},
				},
			},
			map[string]any{"type": "web_search_preview"},
		},
		"tool_choice":      "required",
		"reasoning_effort": "high",
		"temperature":      0.5,
		"max_tokens":       128,
		"top_p":            0.9,
		"user":             "u-1",
	}

	data, path, err := TranslateCodexRequest(body, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("TranslateCodexRequest: %v", err)
	}
	if path != "/codex/responses" {
		t.Fatalf("path = %q, want /codex/responses", path)
	}
	for _, leaked := range []string{`"temperature":`, `"max_tokens":`, `"top_p":`, `"user":`} {
		if strings.Contains(string(data), leaked) {
			t.Fatalf("OpenAI-only field %q leaked into the Codex body: %s", leaked, data)
		}
	}
	if !strings.Contains(string(data), `"stream":true`) || !strings.Contains(string(data), `"store":false`) {
		t.Fatalf("stream/store not hardcoded: %s", data)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req["model"] != "gpt-5.6-sol" {
		t.Errorf("model = %v", req["model"])
	}
	if req["instructions"] != "be terse\n\ndev note" {
		t.Errorf("instructions = %q", req["instructions"])
	}
	if req["parallel_tool_calls"] != true {
		t.Errorf("parallel_tool_calls = %v", req["parallel_tool_calls"])
	}
	if req["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v", req["tool_choice"])
	}

	reasoning, ok := req["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Errorf("reasoning = %v", req["reasoning"])
	}

	input, ok := req["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("input = %s", data)
	}
	user, _ := input[0].(map[string]any)
	if user["role"] != "user" || user["content"] != "hello" {
		t.Errorf("input[0] = %v", user)
	}
	assistant, _ := input[1].(map[string]any)
	if assistant["role"] != "assistant" || assistant["content"] != "hi" {
		t.Errorf("input[1] = %v", assistant)
	}
	call, _ := input[2].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_1" || call["name"] != "get_weather" || call["arguments"] != `{"city":"SF"}` {
		t.Errorf("input[2] = %v", call)
	}
	output, _ := input[3].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_1" || output["output"] != "sunny" {
		t.Errorf("input[3] = %v", output)
	}

	tools, _ := req["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %s", data)
	}
	fn, _ := tools[0].(map[string]any)
	if fn["type"] != "function" || fn["name"] != "get_weather" || fn["strict"] != false {
		t.Errorf("tools[0] = %v, want strict materialized as false", fn)
	}
	if _, ok := fn["parameters"].(map[string]any); !ok {
		t.Errorf("tools[0].parameters missing: %v", fn)
	}
	search, _ := tools[1].(map[string]any)
	if len(search) != 1 || search["type"] != "web_search" {
		t.Errorf("tools[1] = %v, want {\"type\":\"web_search\"}", search)
	}
}

func TestTranslateCodexRequestReasoningAndFormat(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	}

	body := base()
	body["reasoning_effort"] = "none"
	reasoning, _ := decodeCodexRequest(t, body)["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %v, want none/auto", reasoning)
	}

	body = base()
	body["reasoning_effort"] = "minimal"
	reasoning, _ = decodeCodexRequest(t, body)["reasoning"].(map[string]any)
	if reasoning["effort"] != "minimal" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %v, want minimal/auto", reasoning)
	}

	body = base()
	body["reasoning_effort"] = "max"
	reasoning, _ = decodeCodexRequest(t, body)["reasoning"].(map[string]any)
	if reasoning["effort"] != "max" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %v, want max/auto", reasoning)
	}

	body = base()
	body["reasoning_effort"] = "ultra"
	reasoning, _ = decodeCodexRequest(t, body)["reasoning"].(map[string]any)
	if reasoning["effort"] != "ultra" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %v, want ultra/auto", reasoning)
	}

	body = base()
	body["reasoning_effort"] = "turbo"
	reasoning, _ = decodeCodexRequest(t, body)["reasoning"].(map[string]any)
	if reasoning["effort"] != "turbo" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %v, want turbo/auto (verbatim passthrough)", reasoning)
	}

	body = base()
	if req := decodeCodexRequest(t, body); req["reasoning"] != nil {
		t.Fatalf("reasoning must be omitted when unset: %v", req["reasoning"])
	}

	body = base()
	body["response_format"] = map[string]any{
		"type":        "json_schema",
		"json_schema": map[string]any{"name": "out", "strict": true, "schema": map[string]any{"type": "object"}},
	}
	text, _ := decodeCodexRequest(t, body)["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "out" || format["strict"] != true {
		t.Fatalf("text.format = %v", format)
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("text.format.schema missing: %v", format)
	}
}

func TestTranslateCodexRequestImages(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA", "detail": "high"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "one"},
				map[string]any{"type": "text", "text": "two"},
			}},
		},
	}

	req := decodeCodexRequest(t, body)
	input, _ := req["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input = %v", req["input"])
	}

	withImage, _ := input[0].(map[string]any)
	parts, ok := withImage["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("image message content = %v", withImage["content"])
	}
	text, _ := parts[0].(map[string]any)
	if text["type"] != "input_text" || text["text"] != "what is this" {
		t.Errorf("parts[0] = %v", text)
	}
	image, _ := parts[1].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,AAAA" {
		t.Errorf("parts[1] = %v", image)
	}
	if strings.Contains(mustJSON(t, withImage), "detail") {
		t.Errorf("image detail not discarded: %v", withImage)
	}

	plain, _ := input[1].(map[string]any)
	if plain["content"] != "one\ntwo" {
		t.Errorf("text-only parts must join into a string, got %v", plain["content"])
	}
}

func TestTranslateCodexRequestLegacyFunctions(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "function_call": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`}},
			map[string]any{"role": "function", "name": "lookup", "content": "found"},
		},
		"functions": []any{map[string]any{"name": "lookup", "strict": true, "parameters": map[string]any{"type": "object"}}},
	}

	req := decodeCodexRequest(t, body)
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", req["tools"])
	}
	fn, _ := tools[0].(map[string]any)
	if fn["name"] != "lookup" || fn["strict"] != true {
		t.Errorf("tools[0] = %v", fn)
	}

	input, _ := req["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input = %v", req["input"])
	}
	assistant, _ := input[0].(map[string]any)
	if assistant["role"] != "assistant" || assistant["content"] != "" {
		t.Errorf("input[0] = %v, want an empty assistant message before the call", assistant)
	}
	call, _ := input[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "fc_lookup" || call["name"] != "lookup" {
		t.Errorf("input[1] = %v", call)
	}
	output, _ := input[2].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "fc_lookup" || output["output"] != "found" {
		t.Errorf("input[2] = %v", output)
	}
}

func TestTranslateCodexRequestEmptyInput(t *testing.T) {
	req := decodeCodexRequest(t, map[string]any{"messages": []any{map[string]any{"role": "system", "content": "only system"}}})

	if req["instructions"] != "only system" {
		t.Errorf("instructions = %q", req["instructions"])
	}
	input, _ := req["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %v", req["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["role"] != "user" || item["content"] != "" {
		t.Errorf("input[0] = %v, want an empty user message", item)
	}
}

func TestStreamCodexToOpenAI(t *testing.T) {
	w := httptest.NewRecorder()
	if err := StreamCodexToOpenAI(strings.NewReader(codexFixture()), w, "gpt-5.6-sol"); err != nil {
		t.Fatalf("StreamCodexToOpenAI: %v", err)
	}

	body := w.Body.String()
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream does not end with [DONE]:\n%s", body)
	}
	for _, want := range []string{
		`"reasoning_content":"think"`,
		`"content":"Hel"`,
		`"content":"lo"`,
		`"id":"call_1"`,
		`"name":"get_weather"`,
		`"arguments":"{\"city\":"`,
		`"finish_reason":"tool_calls"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q:\n%s", want, body)
		}
	}

	chunks := parseCodexChunks(t, body)
	if len(chunks) == 0 {
		t.Fatal("no chunks emitted")
	}

	var content, reasoning strings.Builder
	var toolID, toolName, arguments string
	var usage map[string]any
	var finish string
	for _, chunk := range chunks {
		if chunk.Object != "chat.completion.chunk" || chunk.Model != "gpt-5.6-sol" {
			t.Errorf("chunk envelope = %+v", chunk)
		}
		if chunk.ID != chunks[0].ID || !strings.HasPrefix(chunk.ID, "chatcmpl-") || len(chunk.ID) != len("chatcmpl-")+24 {
			t.Errorf("chunk id = %q", chunk.ID)
		}
		if len(chunk.Choices) != 1 || chunk.Choices[0].Index != 0 {
			t.Fatalf("choices = %+v", chunk.Choices)
		}

		delta := chunk.Choices[0].Delta
		content.WriteString(delta.Content)
		reasoning.WriteString(delta.ReasoningContent)
		for _, call := range delta.ToolCalls {
			if call.Index != 0 {
				t.Errorf("tool index = %d, want 0", call.Index)
			}
			if call.ID != "" {
				toolID = call.ID
			}
			if call.Type != "" {
				if call.Type != "function" {
					t.Errorf("tool type = %q", call.Type)
				}
			}
			if call.Function.Name != "" {
				toolName = call.Function.Name
			}
			arguments += call.Function.Arguments
		}
		if chunk.Choices[0].FinishReason != nil {
			finish = *chunk.Choices[0].FinishReason
		}
		if len(chunk.Usage) > 0 {
			if err := json.Unmarshal(chunk.Usage, &usage); err != nil {
				t.Fatalf("unmarshal usage: %v", err)
			}
		}
	}

	if chunks[0].Choices[0].Delta.Role != "assistant" || chunks[0].Choices[0].FinishReason != nil {
		t.Errorf("prologue chunk = %+v", chunks[0].Choices[0])
	}
	if content.String() != "Hello" {
		t.Errorf("content = %q", content.String())
	}
	if reasoning.String() != "think" {
		t.Errorf("reasoning = %q", reasoning.String())
	}
	if toolID != "call_1" || toolName != "get_weather" || arguments != `{"city":"SF"}` {
		t.Errorf("tool call = id %q name %q arguments %q", toolID, toolName, arguments)
	}
	if finish != "tool_calls" {
		t.Errorf("finish_reason = %q", finish)
	}
	if usage == nil {
		t.Fatal("no usage emitted")
	}
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(5) || usage["total_tokens"] != float64(15) {
		t.Errorf("usage totals = %v", usage)
	}
	promptDetails, _ := usage["prompt_tokens_details"].(map[string]any)
	if promptDetails["cached_tokens"] != float64(2) {
		t.Errorf("cached_tokens = %v", promptDetails)
	}
	completionDetails, _ := usage["completion_tokens_details"].(map[string]any)
	if completionDetails["reasoning_tokens"] != float64(3) {
		t.Errorf("reasoning_tokens = %v", completionDetails)
	}
}

func TestStreamCodexToOpenAITextOnly(t *testing.T) {
	src := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_2"}}`,
		``,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"hello"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_2","usage":{"input_tokens":3,"output_tokens":1}}}`,
		``,
	}, "\n")

	w := httptest.NewRecorder()
	if err := StreamCodexToOpenAI(strings.NewReader(src), w, "gpt-5.6-sol"); err != nil {
		t.Fatalf("StreamCodexToOpenAI: %v", err)
	}

	body := w.Body.String()
	if strings.Contains(body, "tool_calls") {
		t.Fatalf("text-only stream produced tool calls:\n%s", body)
	}

	chunks := parseCodexChunks(t, body)
	last := chunks[len(chunks)-1]
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %v, want stop", last.Choices[0].FinishReason)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream does not end with [DONE]:\n%s", body)
	}
}

func TestStreamCodexToOpenAIToolCallDoneOnly(t *testing.T) {
	src := strings.Join([]string{
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_9","output_index":0,"arguments":"{\"q\":\"x\"}"}`,
		``,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_9","call_id":"call_9","name":"lookup","arguments":"{\"q\":\"x\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2}}}`,
		``,
	}, "\n")

	w := httptest.NewRecorder()
	if err := StreamCodexToOpenAI(strings.NewReader(src), w, "gpt-5.6-sol"); err != nil {
		t.Fatalf("StreamCodexToOpenAI: %v", err)
	}

	var arguments, toolID, toolName string
	for _, chunk := range parseCodexChunks(t, w.Body.String()) {
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			if call.ID != "" {
				toolID = call.ID
			}
			if call.Function.Name != "" {
				toolName = call.Function.Name
			}
			arguments += call.Function.Arguments
		}
	}

	if arguments != `{"q":"x"}` {
		t.Fatalf("arguments = %q, want them emitted exactly once", arguments)
	}
	if toolID != "call_9" || toolName != "lookup" {
		t.Fatalf("tool call = id %q name %q", toolID, toolName)
	}
	if !strings.Contains(w.Body.String(), `"finish_reason":"tool_calls"`) {
		t.Fatalf("finish_reason = tool_calls missing:\n%s", w.Body.String())
	}
}

func TestBufferCodexToOpenAI(t *testing.T) {
	data, err := BufferCodexToOpenAI(strings.NewReader(codexFixture()), "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("BufferCodexToOpenAI: %v", err)
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "chat.completion" || resp.Model != "gpt-5.6-sol" || !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Fatalf("envelope = %+v", resp)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %+v", resp.Choices)
	}

	choice := resp.Choices[0]
	if choice.Message.Content != "Hello" || choice.Message.ReasoningContent != "think" {
		t.Errorf("message = %+v", choice.Message)
	}
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", choice.Message.ToolCalls)
	}
	call := choice.Message.ToolCalls[0]
	if call.Index != 0 || call.ID != "call_1" || call.Type != "function" || call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"SF"}` {
		t.Errorf("tool call = %+v", call)
	}

	var usage map[string]any
	if err := json.Unmarshal(resp.Usage, &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(5) || usage["total_tokens"] != float64(15) {
		t.Errorf("usage = %v", usage)
	}
}

func TestStreamCodexToAnthropicSSE(t *testing.T) {
	before := runtime.NumGoroutine()
	w := httptest.NewRecorder()

	StreamCodexToAnthropicSSE(strings.NewReader(codexFixture()), w, "gpt-5.6-sol")

	body := w.Body.String()
	for _, want := range []string{
		`event: message_start`,
		`event: content_block_start`,
		`event: content_block_delta`,
		`"type":"thinking_delta"`,
		`"type":"text_delta"`,
		`"type":"input_json_delta"`,
		`"id":"call_1"`,
		`"name":"get_weather"`,
		`event: message_delta`,
		`"stop_reason":"tool_use"`,
		`event: message_stop`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q:\n%s", want, body)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("producer goroutine still alive: %d goroutines before, %d afterwards", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamCodexToOpenAIErrorEvent(t *testing.T) {
	cases := map[string]string{
		"error": `data: {"type":"error","code":"server_error","message":"boom"}`,
		"response.failed": `data: {"type":"response.failed","response":{"status":"failed",` +
			`"error":{"code":"server_error","message":"boom"}}}`,
	}

	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_3"}}`,
				``,
				frame,
				``,
				`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"nope"}`,
				``,
			}, "\n")

			w := httptest.NewRecorder()
			if err := StreamCodexToOpenAI(strings.NewReader(src), w, "gpt-5.6-sol"); err != nil {
				t.Fatalf("upstream-reported error must not surface as an adapter error: %v", err)
			}

			body := w.Body.String()
			for _, want := range []string{`"type":"upstream_error"`, `"message":"boom"`, `"code":"server_error"`} {
				if !strings.Contains(body, want) {
					t.Errorf("output missing %q:\n%s", want, body)
				}
			}
			if strings.Contains(body, "nope") {
				t.Fatalf("kept reading after the error frame:\n%s", body)
			}
			if !strings.HasSuffix(body, "data: [DONE]\n\n") {
				t.Fatalf("stream does not end with [DONE]:\n%s", body)
			}

			if _, err := BufferCodexToOpenAI(strings.NewReader(src), "gpt-5.6-sol"); err == nil {
				t.Fatal("BufferCodexToOpenAI must report the upstream error")
			}
		})
	}
}
