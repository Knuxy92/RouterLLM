package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTranslateGoogleRequestBasic(t *testing.T) {
	body := map[string]any{
		"model": "ignored",
		"messages": []any{
			map[string]any{"role": "system", "content": "be terse"},
			map[string]any{"role": "user", "content": "hello"},
			map[string]any{"role": "assistant", "content": "hi there"},
			map[string]any{"role": "user", "content": "bye"},
		},
		"temperature": 0.5,
	}

	data, path, err := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("TranslateGoogleRequest: %v", err)
	}
	if path != "/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse" {
		t.Fatalf("path = %q", path)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := req["model"]; ok {
		t.Fatalf("payload must not carry model: %s", data)
	}
	sys, ok := req["systemInstruction"].(map[string]any)
	if !ok || !strings.Contains(mustJSON(t, sys), "be terse") {
		t.Fatalf("systemInstruction missing: %s", data)
	}
	contents, ok := req["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("contents = %s", data)
	}
	first, _ := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("contents[0].role = %v, want user", first["role"])
	}
	second, _ := contents[1].(map[string]any)
	if second["role"] != "model" {
		t.Fatalf("contents[1].role = %v, want model", second["role"])
	}
}

func TestTranslateGoogleRequestToolsAndChoice(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "look up weather",
				"parameters": map[string]any{
					"$schema":              "http://json-schema.org/draft/schema",
					"additionalProperties": false,
					"type":                 "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string", "description": "city name"},
						"unit": map[string]any{"type": []any{"string", "null"}},
					},
					"required": []any{"city"},
				},
			},
		}},
		"tool_choice": "required",
	}

	data, _, err := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("TranslateGoogleRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %s", data)
	}
	decls, _ := tools[0].(map[string]any)["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("functionDeclarations = %s", data)
	}
	decl := mustJSON(t, decls[0])
	if strings.Contains(decl, "$schema") || strings.Contains(decl, "additionalProperties") {
		t.Fatalf("schema keywords not stripped: %s", decl)
	}
	if !strings.Contains(decl, `"type":"object"`) {
		t.Fatalf("type not lowercased: %s", decl)
	}
	if !strings.Contains(decl, `"nullable":true`) {
		t.Fatalf("null union not converted: %s", decl)
	}

	tc, _ := req["toolConfig"].(map[string]any)
	if tc == nil {
		t.Fatalf("toolConfig missing: %s", data)
	}
	if got := mustJSON(t, tc); !strings.Contains(got, `"mode":"ANY"`) {
		t.Fatalf("toolConfig = %s, want ANY", got)
	}

	body["tool_choice"] = map[string]any{"function": map[string]any{"name": "get_weather"}}
	data, _, _ = TranslateGoogleRequest(body, "gemini-3.8-flash")
	if !strings.Contains(string(data), `"allowed_function_names":["get_weather"]`) {
		t.Fatalf("allowed_function_names missing: %s", data)
	}
}

func TestTranslateGoogleRequestToolHistory(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "weather?"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id":   "call_1",
				"type": "function",
				"function": map[string]any{
					"name":      "get_weather",
					"arguments": `{"city":"BKK"}`,
				},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": `{"temp":32}`},
		},
	}

	data, _, err := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("TranslateGoogleRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %s", data)
	}
	modelMsg := mustJSON(t, contents[1])
	if !strings.Contains(modelMsg, `"name":"get_weather"`) || !strings.Contains(modelMsg, `"args":{"city":"BKK"}`) {
		t.Fatalf("functionCall part wrong: %s", modelMsg)
	}
	toolMsg := mustJSON(t, contents[2])
	if !strings.Contains(toolMsg, `"name":"get_weather"`) || !strings.Contains(toolMsg, `"result":{"temp":32}`) {
		t.Fatalf("functionResponse part wrong: %s", toolMsg)
	}
	if strings.Contains(toolMsg, `"role":"model"`) {
		t.Fatalf("tool result must be role user: %s", toolMsg)
	}
}

func TestTranslateGoogleRequestThinking(t *testing.T) {
	body := map[string]any{
		"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
		"reasoning_effort": "high",
	}
	data, _, _ := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if !strings.Contains(string(data), `"thinkingConfig":{"thinkingLevel":"high"}`) {
		t.Fatalf("thinkingLevel missing: %s", data)
	}

	body["reasoning_effort"] = "max"
	data, _, _ = TranslateGoogleRequest(body, "gemini-3.8-flash")
	if !strings.Contains(string(data), `"thinkingLevel":"high"`) {
		t.Fatalf("max not mapped to high: %s", data)
	}

	body["reasoning_effort"] = "none"
	data, _, _ = TranslateGoogleRequest(body, "gemini-3.8-flash")
	if !strings.Contains(string(data), `"thinkingBudget":0`) {
		t.Fatalf("none not mapped to budget 0: %s", data)
	}

	body["reasoning_effort"] = "high"
	body["thinking_budget"] = 4096
	data, _, _ = TranslateGoogleRequest(body, "gemini-3.8-flash")
	if !strings.Contains(string(data), `"thinkingBudget":4096`) {
		t.Fatalf("thinkingBudget missing: %s", data)
	}
	if strings.Contains(string(data), "thinkingLevel") {
		t.Fatalf("budget must suppress level: %s", data)
	}
}

func TestTranslateGoogleRequestGenerationConfig(t *testing.T) {
	body := map[string]any{
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens":  256,
		"stop":        []any{"END", "STOP"},
		"top_p":       0.9,
		"temperature": 0.2,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{"schema": map[string]any{
				"type": "OBJECT", "properties": map[string]any{"a": map[string]any{"type": "STRING"}},
			}},
		},
	}

	data, _, err := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("TranslateGoogleRequest: %v", err)
	}
	for _, want := range []string{
		`"maxOutputTokens":256`,
		`"stopSequences":["END","STOP"]`,
		`"responseMimeType":"application/json"`,
		`"responseSchema":{"properties":{"a":{"type":"string"}},"type":"object"}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in %s", want, data)
		}
	}
}

func TestTranslateGoogleRequestImageDataURL(t *testing.T) {
	const pngB64 = "aGVsbG8="
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "what is this"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + pngB64}},
		}}},
	}

	data, _, err := TranslateGoogleRequest(body, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("TranslateGoogleRequest: %v", err)
	}
	if !strings.Contains(string(data), `"mime_type":"image/png"`) || !strings.Contains(string(data), `"data":"`+pngB64+`"`) {
		t.Fatalf("inline_data missing: %s", data)
	}
}

const googleStreamFixture = "data: {\"responseId\":\"resp-1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"plan:\"},{\"text\":\" secret plan\",\"thought\":true}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5,\"totalTokenCount\":18,\"thoughtsTokenCount\":3}}\n\n" +
	"data: {\"responseId\":\"resp-1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":9,\"totalTokenCount\":22,\"thoughtsTokenCount\":3}}\n\n" +
	"data: {\"responseId\":\"resp-1\",\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"get_weather\",\"args\":{\"city\":\"BKK\"}}}],\"role\":\"model\"}}]}\n\n" +
	"data: {\"responseId\":\"resp-1\",\"candidates\":[{\"content\":{\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":12,\"totalTokenCount\":25,\"thoughtsTokenCount\":3}}\n\n"

func TestStreamGoogleToOpenAI(t *testing.T) {
	w := httptest.NewRecorder()
	if err := StreamGoogleToOpenAI(strings.NewReader(googleStreamFixture), w, "gemini-3.8-flash"); err != nil {
		t.Fatalf("StreamGoogleToOpenAI: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, `"delta":{"role":"assistant"}`) {
		t.Fatalf("role chunk missing:\n%s", body)
	}
	if !strings.Contains(body, `"content":"answer"`) {
		t.Fatalf("text delta missing:\n%s", body)
	}
	if !strings.Contains(body, `"reasoning_content":" secret plan"`) {
		t.Fatalf("thought delta missing:\n%s", body)
	}
	if !strings.Contains(body, `"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"BKK\"}"}}]`) {
		t.Fatalf("tool_calls delta missing:\n%s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("finish chunk missing:\n%s", body)
	}
	if !strings.Contains(body, `"completion_tokens":15`) {
		t.Fatalf("usage missing (candidates 12 + thoughts 3):\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with [DONE]:\n%s", body)
	}
}

func TestBufferGoogleToOpenAI(t *testing.T) {
	data, err := BufferGoogleToOpenAI(strings.NewReader(googleStreamFixture), "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("BufferGoogleToOpenAI: %v", err)
	}

	var resp struct {
		ID      string `json:"id"`
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
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, data)
	}
	if resp.ID != "resp-1" {
		t.Fatalf("id = %q, want resp-1", resp.ID)
	}
	msg := resp.Choices[0].Message
	if msg.Role != "assistant" || msg.Content != "plan:answer" || msg.ReasoningContent != " secret plan" {
		t.Fatalf("message = %+v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" || msg.ToolCalls[0].Function.Arguments != `{"city":"BKK"}` {
		t.Fatalf("tool_calls = %+v", msg.ToolCalls)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage.CompletionTokens != 15 || resp.Usage.PromptTokens != 10 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestStreamGoogleFinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"STOP":       "stop",
		"MAX_TOKENS": "length",
		"SAFETY":     "content_filter",
		"RECITATION": "content_filter",
	}
	for upstream, want := range cases {
		sse := "data: {\"candidates\":[{\"content\":{\"role\":\"model\"},\"finishReason\":\"" + upstream + "\"}]}\n\n"
		w := httptest.NewRecorder()
		if err := StreamGoogleToOpenAI(strings.NewReader(sse), w, "m"); err != nil {
			t.Fatalf("StreamGoogleToOpenAI: %v", err)
		}
		if !strings.Contains(w.Body.String(), `"finish_reason":"`+want+`"`) {
			t.Fatalf("%s → want %s, got:\n%s", upstream, want, w.Body.String())
		}
	}
}

func TestStreamGoogleBlockedPrompt(t *testing.T) {
	sse := "data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}}\n\n"
	w := httptest.NewRecorder()
	if err := StreamGoogleToOpenAI(strings.NewReader(sse), w, "m"); err != nil {
		t.Fatalf("StreamGoogleToOpenAI: %v", err)
	}
	if !strings.Contains(w.Body.String(), `"finish_reason":"content_filter"`) {
		t.Fatalf("blocked prompt not surfaced:\n%s", w.Body.String())
	}
}

func TestStreamGoogleToAnthropicSSE(t *testing.T) {
	sse := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}],\"role\":\"model\"}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\"},\"finishReason\":\"STOP\"}]}\n\n"
	w := httptest.NewRecorder()
	if err := StreamGoogleToAnthropicSSE(strings.NewReader(sse), w, "gemini-3.8-flash"); err != nil {
		t.Fatalf("StreamGoogleToAnthropicSSE: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Fatalf("message_start missing:\n%s", body)
	}
	if !strings.Contains(body, `"text":"hello","type":"text_delta"`) {
		t.Fatalf("text delta missing:\n%s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop missing:\n%s", body)
	}
}

const googleFinishWithTrailingChunks = "data: {\"responseId\":\"resp-leak\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"working\"}],\"role\":\"model\"}}]}\n\n" +
	"data: {\"responseId\":\"resp-leak\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" on it\"}],\"role\":\"model\"}}]}\n\n" +
	"data: {\"responseId\":\"resp-leak\",\"candidates\":[{\"content\":{\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":12,\"totalTokenCount\":25,\"thoughtsTokenCount\":3}}\n\n" +
	"data: {\"responseId\":\"resp-leak\",\"candidates\":[{\"content\":{\"role\":\"model\"}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":12,\"totalTokenCount\":25,\"thoughtsTokenCount\":3}}\n\n" +
	"data: [DONE]\n\n"

// TestStreamGoogleToAnthropicSSENoGoroutineLeak guards the pipe lifecycle:
// the converter stops reading at finish_reason while the producer still has
// chunks to write, so the pipe reader must be closed to release the producer
// instead of leaving it blocked on pw.Write forever.
func TestStreamGoogleToAnthropicSSENoGoroutineLeak(t *testing.T) {
	runtime.GC()
	time.Sleep(20 * time.Millisecond)

	before := runtime.NumGoroutine()
	w := httptest.NewRecorder()
	if err := StreamGoogleToAnthropicSSE(strings.NewReader(googleFinishWithTrailingChunks), w, "gemini-3.8-flash"); err != nil {
		t.Fatalf("StreamGoogleToAnthropicSSE: %v", err)
	}
	if !strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("converter did not reach message_stop:\n%s", w.Body.String())
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

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}
