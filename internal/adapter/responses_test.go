package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranslateResponsesRequest(t *testing.T) {
	body := map[string]any{
		"model": "ignored",
		"messages": []any{
			map[string]any{"role": "system", "content": "be terse"},
			map[string]any{"role": "developer", "content": []any{
				map[string]any{"type": "text", "text": "no markdown"},
			}},
			map[string]any{"role": "user", "content": "weather?"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"BKK"}`,
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": `{"temp":32}`},
		},
		"max_tokens":       256,
		"temperature":      0.5,
		"top_p":            0.9,
		"reasoning_effort": "high",
		"tool_choice":      map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
		"stream_options":   map[string]any{"include_usage": true},
		"stop":             []any{"END"},
		"n":                2,
		"logprobs":         true,
		"reasoning":        map[string]any{"effort": "low"},
		"enable_thinking":  true,
		"thinking_budget":  1024,
		"thinking":         map[string]any{"type": "enabled"},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name": "get_weather", "description": "look up weather",
				"parameters": map[string]any{"type": "object"},
			}},
			map[string]any{"type": "web_search"},
		},
	}

	data, path, err := TranslateResponsesRequest(body, "gpt-5.2")
	if err != nil {
		t.Fatalf("TranslateResponsesRequest: %v", err)
	}
	if path != "/v1/responses" {
		t.Fatalf("path = %q", path)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req["model"] != "gpt-5.2" {
		t.Fatalf("model = %v", req["model"])
	}
	if req["instructions"] != "be terse\n\nno markdown" {
		t.Fatalf("instructions = %v", req["instructions"])
	}
	if req["stream"] != true {
		t.Fatalf("stream must be forced true: %s", data)
	}
	if got := mustJSON(t, req["max_output_tokens"]); got != "256" {
		t.Fatalf("max_output_tokens = %s: %s", got, data)
	}
	if !strings.Contains(mustJSON(t, req["reasoning"]), `"effort":"high"`) {
		t.Fatalf("reasoning missing: %s", data)
	}
	if req["temperature"] != 0.5 || req["top_p"] != 0.9 {
		t.Fatalf("sampling params = %s", data)
	}

	input, ok := req["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input = %s", data)
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "weather?" {
		t.Fatalf("input[0] = %s", mustJSON(t, first))
	}
	second, _ := input[1].(map[string]any)
	callJSON := mustJSON(t, second)
	for _, want := range []string{`"type":"function_call"`, `"call_id":"call_1"`, `"name":"get_weather"`, `"arguments":"{\"city\":\"BKK\"}"`} {
		if !strings.Contains(callJSON, want) {
			t.Fatalf("input[1] missing %s: %s", want, callJSON)
		}
	}
	third, _ := input[2].(map[string]any)
	outJSON := mustJSON(t, third)
	for _, want := range []string{`"type":"function_call_output"`, `"call_id":"call_1"`, `"output":"{\"temp\":32}"`} {
		if !strings.Contains(outJSON, want) {
			t.Fatalf("input[2] missing %s: %s", want, outJSON)
		}
	}

	tools, _ := req["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %s", data)
	}
	toolJSON := mustJSON(t, tools)
	for _, want := range []string{`"name":"get_weather"`, `"description":"look up weather"`, `"parameters":{"type":"object"}`, `"type":"web_search"`} {
		if !strings.Contains(toolJSON, want) {
			t.Fatalf("tools missing %s: %s", want, toolJSON)
		}
	}
	if strings.Contains(toolJSON, `"function":`) {
		t.Fatalf("tools not flattened: %s", toolJSON)
	}

	tcJSON := mustJSON(t, req["tool_choice"])
	if !strings.Contains(tcJSON, `"name":"get_weather"`) || strings.Contains(tcJSON, `"function":`) {
		t.Fatalf("tool_choice not flattened: %s", tcJSON)
	}

	for _, key := range []string{"stream_options", "stop", "n", "logprobs", "enable_thinking", "thinking_budget", "thinking"} {
		if _, ok := req[key]; ok {
			t.Fatalf("%s must be dropped: %s", key, data)
		}
	}
}

func TestTranslateResponsesRequestContentParts(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/cat.png"}},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}},
			}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "a cat"},
			}},
		},
		"tool_choice": "auto",
	}

	data, _, err := TranslateResponsesRequest(body, "gpt-5.2")
	if err != nil {
		t.Fatalf("TranslateResponsesRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	input, ok := req["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %s", data)
	}
	userJSON := mustJSON(t, input[0])
	for _, want := range []string{
		`"role":"user"`,
		`"type":"input_text"`, `"text":"what is this"`,
		`"type":"input_image"`, `"image_url":"https://example.com/cat.png"`,
		`"image_url":"data:image/png;base64,aGVsbG8="`,
	} {
		if !strings.Contains(userJSON, want) {
			t.Fatalf("user item missing %s: %s", want, userJSON)
		}
	}
	assistantJSON := mustJSON(t, input[1])
	for _, want := range []string{`"role":"assistant"`, `"type":"output_text"`, `"text":"a cat"`} {
		if !strings.Contains(assistantJSON, want) {
			t.Fatalf("assistant item missing %s: %s", want, assistantJSON)
		}
	}
	if req["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", req["tool_choice"])
	}

	body["reasoning_effort"] = "none"
	data, _, _ = TranslateResponsesRequest(body, "gpt-5.2")
	if strings.Contains(string(data), `"reasoning"`) {
		t.Fatalf("effort none must omit reasoning: %s", data)
	}
}

const responsesStreamFixture = `data: {"type":"response.created","response":{"id":"resp_123","created_at":1700000000}}

data: {"type":"response.output_text.delta","delta":"Hello"}

data: {"type":"response.output_text.delta","delta":" world"}

data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_abc","name":"get_weather"}}

data: {"type":"response.function_call_arguments.delta","delta":"{\"city\":"}

data: {"type":"response.function_call_arguments.delta","delta":"\"BKK\"}"}

data: {"type":"response.completed","response":{"id":"resp_123","created_at":1700000000,"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`

func parseResponsesChunks(t *testing.T, body string) []map[string]any {
	t.Helper()
	var chunks []map[string]any
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[len("data: "):]
		if payload == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("unmarshal chunk %q: %v", payload, err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func responsesChoiceDelta(t *testing.T, chunk map[string]any) map[string]any {
	t.Helper()
	choices, ok := chunk["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("chunk without choices: %s", mustJSON(t, chunk))
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	return delta
}

func TestStreamResponsesToOpenAI(t *testing.T) {
	w := httptest.NewRecorder()
	if err := StreamResponsesToOpenAI(strings.NewReader(responsesStreamFixture), w, "gpt-5.2"); err != nil {
		t.Fatalf("StreamResponsesToOpenAI: %v", err)
	}

	body := w.Body.String()
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with [DONE]:\n%s", body)
	}
	chunks := parseResponsesChunks(t, body)
	if len(chunks) != 8 {
		t.Fatalf("expected 8 chunks, got %d:\n%s", len(chunks), body)
	}

	if got := responsesChoiceDelta(t, chunks[0]); got["role"] != "assistant" {
		t.Fatalf("first chunk delta = %s", mustJSON(t, got))
	}
	if chunks[0]["id"] != "resp_123" || chunks[0]["model"] != "gpt-5.2" {
		t.Fatalf("first chunk = %s", mustJSON(t, chunks[0]))
	}
	if created, ok := chunks[0]["created"].(float64); !ok || int64(created) != 1700000000 {
		t.Fatalf("created = %v", chunks[0]["created"])
	}

	if got := responsesChoiceDelta(t, chunks[1]); got["content"] != "Hello" {
		t.Fatalf("chunks[1] delta = %s", mustJSON(t, got))
	}
	if got := responsesChoiceDelta(t, chunks[2]); got["content"] != " world" {
		t.Fatalf("chunks[2] delta = %s", mustJSON(t, got))
	}

	tcs, ok := responsesChoiceDelta(t, chunks[3])["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("chunks[3] missing tool_calls: %s", mustJSON(t, chunks[3]))
	}
	call, _ := tcs[0].(map[string]any)
	callJSON := mustJSON(t, call)
	for _, want := range []string{`"index":0`, `"id":"call_abc"`, `"type":"function"`, `"name":"get_weather"`, `"arguments":""`} {
		if !strings.Contains(callJSON, want) {
			t.Fatalf("tool call missing %s: %s", want, callJSON)
		}
	}

	for i, want := range []string{`{"city":`, "\"BKK\"}"} {
		tcs, ok := responsesChoiceDelta(t, chunks[4+i])["tool_calls"].([]any)
		if !ok || len(tcs) != 1 {
			t.Fatalf("chunks[%d] missing tool_calls", 4+i)
		}
		call, _ := tcs[0].(map[string]any)
		if call["index"] != float64(0) {
			t.Fatalf("argument delta[%d] index = %v", i, call["index"])
		}
		if _, has := call["id"]; has {
			t.Fatalf("argument delta[%d] must omit id: %s", i, mustJSON(t, call))
		}
		if _, has := call["type"]; has {
			t.Fatalf("argument delta[%d] must omit type: %s", i, mustJSON(t, call))
		}
		fn, _ := call["function"].(map[string]any)
		if _, has := fn["name"]; has {
			t.Fatalf("argument delta[%d] must omit name: %s", i, mustJSON(t, call))
		}
		if fn["arguments"] != want {
			t.Fatalf("argument delta[%d] arguments = %v, want %q", i, fn["arguments"], want)
		}
	}

	finishJSON := mustJSON(t, chunks[6])
	if !strings.Contains(finishJSON, `"delta":{}`) || !strings.Contains(finishJSON, `"finish_reason":"stop"`) {
		t.Fatalf("finish chunk = %s", finishJSON)
	}
	usageJSON := mustJSON(t, chunks[7])
	for _, want := range []string{`"finish_reason":null`, `"prompt_tokens":10`, `"completion_tokens":5`, `"total_tokens":15`} {
		if !strings.Contains(usageJSON, want) {
			t.Fatalf("usage chunk missing %s: %s", want, usageJSON)
		}
	}
}

func TestBufferResponsesToOpenAI(t *testing.T) {
	data, err := BufferResponsesToOpenAI(strings.NewReader(responsesStreamFixture), "gpt-5.2")
	if err != nil {
		t.Fatalf("BufferResponsesToOpenAI: %v", err)
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, data)
	}
	if resp.ID != "resp_123" || resp.Object != "chat.completion" || resp.Model != "gpt-5.2" || resp.Created != 1700000000 {
		t.Fatalf("envelope = %s", data)
	}
	msg := resp.Choices[0].Message
	if msg.Role != "assistant" || msg.Content != "Hello world" {
		t.Fatalf("message = %s", data)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_abc" || msg.ToolCalls[0].Type != "function" ||
		msg.ToolCalls[0].Function.Name != "get_weather" || msg.ToolCalls[0].Function.Arguments != `{"city":"BKK"}` {
		t.Fatalf("tool_calls = %s", data)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %s", data)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %s", data)
	}
}

func TestResponsesReasoningDeltas(t *testing.T) {
	sse := `data: {"type":"response.created","response":{"id":"resp_r","created_at":1700000001}}

data: {"type":"response.reasoning_summary_text.delta","delta":"thinking "}

data: {"type":"response.reasoning_text.delta","delta":"hard"}

data: {"type":"response.output_text.delta","delta":"answer"}

data: {"type":"response.completed","response":{"id":"resp_r","status":"completed"}}

`
	w := httptest.NewRecorder()
	if err := StreamResponsesToOpenAI(strings.NewReader(sse), w, "gpt-5.2"); err != nil {
		t.Fatalf("StreamResponsesToOpenAI: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"reasoning_content":"thinking "`) || !strings.Contains(body, `"reasoning_content":"hard"`) {
		t.Fatalf("reasoning deltas missing:\n%s", body)
	}

	data, err := BufferResponsesToOpenAI(strings.NewReader(sse), "gpt-5.2")
	if err != nil {
		t.Fatalf("BufferResponsesToOpenAI: %v", err)
	}
	if !strings.Contains(string(data), `"reasoning_content":"thinking hard"`) {
		t.Fatalf("buffered reasoning missing: %s", data)
	}
	if !strings.Contains(string(data), `"content":"answer"`) {
		t.Fatalf("buffered content missing: %s", data)
	}
}

func TestResponsesIncompleteMapsToLength(t *testing.T) {
	sse := `data: {"type":"response.created","response":{"id":"resp_i","created_at":1700000003}}

data: {"type":"response.output_text.delta","delta":"partial"}

data: {"type":"response.completed","response":{"id":"resp_i","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":3,"output_tokens":9,"total_tokens":12}}}

`
	w := httptest.NewRecorder()
	if err := StreamResponsesToOpenAI(strings.NewReader(sse), w, "gpt-5.2"); err != nil {
		t.Fatalf("StreamResponsesToOpenAI: %v", err)
	}
	if !strings.Contains(w.Body.String(), `"finish_reason":"length"`) {
		t.Fatalf("length finish missing:\n%s", w.Body.String())
	}

	data, err := BufferResponsesToOpenAI(strings.NewReader(sse), "gpt-5.2")
	if err != nil {
		t.Fatalf("BufferResponsesToOpenAI: %v", err)
	}
	if !strings.Contains(string(data), `"finish_reason":"length"`) {
		t.Fatalf("buffered length finish missing: %s", data)
	}
}

func TestResponsesFailedEmitsError(t *testing.T) {
	failed := `data: {"type":"response.created","response":{"id":"resp_f","created_at":1700000002}}

data: {"type":"response.failed","response":{"id":"resp_f","status":"failed","error":{"code":"server_error","message":"boom"}}}

`
	w := httptest.NewRecorder()
	err := StreamResponsesToOpenAI(strings.NewReader(failed), w, "gpt-5.2")
	if err == nil || !strings.Contains(err.Error(), "responses upstream error: boom") {
		t.Fatalf("err = %v, want responses upstream error: boom", err)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("failed stream must not emit [DONE]:\n%s", w.Body.String())
	}

	bare := `data: {"type":"error","message":"bad key"}

`
	w = httptest.NewRecorder()
	err = StreamResponsesToOpenAI(strings.NewReader(bare), w, "gpt-5.2")
	if err == nil || !strings.Contains(err.Error(), "responses upstream error: bad key") {
		t.Fatalf("err = %v, want responses upstream error: bad key", err)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("error stream must not emit [DONE]:\n%s", w.Body.String())
	}
}

func TestBufferResponsesEmptyStream(t *testing.T) {
	data, err := BufferResponsesToOpenAI(strings.NewReader(""), "gpt-5.2")
	if err != nil {
		t.Fatalf("BufferResponsesToOpenAI: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, data)
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v: %s", resp["object"], data)
	}
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) != 0 {
		t.Fatalf("choices = %s", data)
	}
}

func TestStreamResponsesToAnthropicSSE(t *testing.T) {
	sse := `data: {"type":"response.created","response":{"id":"resp_a","created_at":1700000004}}

data: {"type":"response.output_text.delta","delta":"hello"}

data: {"type":"response.completed","response":{"id":"resp_a","status":"completed","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}

`
	w := httptest.NewRecorder()
	if err := StreamResponsesToAnthropicSSE(strings.NewReader(sse), w, "gpt-5.2"); err != nil {
		t.Fatalf("StreamResponsesToAnthropicSSE: %v", err)
	}

	body := w.Body.String()
	for _, want := range []string{"event: message_start", `"text":"hello","type":"text_delta"`, "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}
