package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamOpenAIToAnthropicSSEHandlesRolelessBlocks(t *testing.T) {
	src := strings.NewReader(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{"content":"answer"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	w := httptest.NewRecorder()

	StreamOpenAIToAnthropicSSE(src, w, "test-model")

	body := w.Body.String()
	for _, want := range []string{
		`event: message_start`,
		`"index":0,"type":"content_block_start"`,
		`"thinking":"think","type":"thinking_delta"`,
		`"index":0,"type":"content_block_stop"`,
		`"index":1,"type":"content_block_start"`,
		`"text":"answer","type":"text_delta"`,
		`"index":1,"type":"content_block_stop"`,
		`event: message_stop`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q:\n%s", want, body)
		}
	}
}

func TestStreamToolCallsWithLiveInputJSONDelta(t *testing.T) {
	src := strings.NewReader(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"San Francisco\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	w := httptest.NewRecorder()

	StreamOpenAIToAnthropicSSE(src, w, "test-model")

	body := w.Body.String()
	if !strings.Contains(body, `event: message_start`) {
		t.Fatal("missing message_start")
	}
	if !strings.Contains(body, `"id":"call_1"`) || !strings.Contains(body, `"name":"get_weather"`) {
		t.Fatal("missing tool_use content_block_start with id/name")
	}
	n := strings.Count(body, `"type":"input_json_delta"`)
	if n != 2 {
		t.Fatalf("expected 2 input_json_delta events, got %d:\n%s", n, body)
	}
	if !strings.Contains(body, `"type":"content_block_stop"`) {
		t.Fatal("missing content_block_stop for tool")
	}
}

func TestStreamToolCallsInSingleChunk(t *testing.T) {
	src := strings.NewReader(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"SF\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	w := httptest.NewRecorder()

	StreamOpenAIToAnthropicSSE(src, w, "test-model")

	body := w.Body.String()
	if !strings.Contains(body, `"id":"call_1"`) || !strings.Contains(body, `"name":"get_weather"`) {
		t.Fatalf("missing tool_use block:\n%s", body)
	}
	n := strings.Count(body, `"type":"input_json_delta"`)
	if n != 1 {
		t.Fatalf("expected 1 input_json_delta, got %d:\n%s", n, body)
	}
}

func TestStreamUsageInFinalChunk(t *testing.T) {
	w := httptest.NewRecorder()
	src := strings.NewReader(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	StreamOpenAIToAnthropicSSE(src, w, "test-model")

	body := w.Body.String()
	if !strings.Contains(body, `"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}`) {
		t.Errorf("message_delta missing usage:\n%s", body)
	}
}

func TestStreamMultipleToolCalls(t *testing.T) {
	chunks := []string{
		`data: {"id":"chat-1","choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","function":{"name":"fn1","arguments":"{\"a\":1}"}},{"index":1,"id":"call_2","function":{"name":"fn2","arguments":"{\"b\":2}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}
	src := strings.NewReader(strings.Join(chunks, "\n"))
	w := httptest.NewRecorder()

	StreamOpenAIToAnthropicSSE(src, w, "test-model")

	body := w.Body.String()
	if strings.Count(body, `"type":"content_block_start"`) != 2 {
		t.Fatalf("expected 2 content_block_start events:\n%s", body)
	}
	if strings.Count(body, `"type":"input_json_delta"`) != 2 {
		t.Fatalf("expected 2 input_json_delta events:\n%s", body)
	}
	if strings.Count(body, `"type":"content_block_stop"`) != 2 {
		t.Fatalf("expected 2 content_block_stop events:\n%s", body)
	}
}

func TestStreamToolCallsLogsMultiChoiceWarning(t *testing.T) {
	src := strings.NewReader(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null},{"index":1,"delta":{"content":"b"},"finish_reason":null}],"finish_reason":null}`,
		``,
		`data: {"id":"chat-1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	w := httptest.NewRecorder()
	StreamOpenAIToAnthropicSSE(src, w, "test-model")
	body := w.Body.String()
	if !strings.Contains(body, `"text":"a"`) {
		t.Errorf("expected first choice content only:\n%s", body)
	}
}

func TestTranslateRequestCarriesTools(t *testing.T) {
	body := map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name":        "get_weather",
				"description": "weather",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
			}},
			map[string]any{"type": "function", "function": map[string]any{"name": "no_params"}},
		},
		"tool_choice": "required",
	}

	data, path, err := TranslateRequest(body, "claude-upstream")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if path != "/v1/messages" {
		t.Fatalf("path = %q", path)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	tools, _ := req["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %v, want both client tools carried over", req["tools"])
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "get_weather" || first["description"] != "weather" {
		t.Errorf("first tool = %v, want name/description carried", first)
	}
	schema, ok := first["input_schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Errorf("input_schema = %v, want the client parameters moved across", first["input_schema"])
	}
	second, _ := tools[1].(map[string]any)
	if schema, ok := second["input_schema"].(map[string]any); !ok || schema["type"] != "object" {
		t.Errorf("tool without parameters = %v, want an empty object schema", second)
	}

	choice, _ := req["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Errorf("tool_choice = %v, want {type:any} for required", req["tool_choice"])
	}
}

func TestTranslateRequestFoldsToolHistory(t *testing.T) {
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "user", "content": "weather?"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"Paris"}`,
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "18C"},
			map[string]any{"role": "user", "content": "thanks"},
		},
	}

	data, _, err := TranslateRequest(body, "claude-upstream")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	messages, _ := req["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want user / assistant / tool_result / user: %v", len(messages), messages)
	}

	assistant, _ := messages[1].(map[string]any)
	blocks, _ := assistant["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("assistant content = %v, want one tool_use block", assistant["content"])
	}
	toolUse, _ := blocks[0].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call_1" || toolUse["name"] != "get_weather" {
		t.Errorf("tool_use = %v, want the assistant call carried across", toolUse)
	}
	input, _ := toolUse["input"].(map[string]any)
	if input["city"] != "Paris" {
		t.Errorf("tool_use input = %v, want the parsed arguments", toolUse["input"])
	}

	resultTurn, _ := messages[2].(map[string]any)
	if role, _ := resultTurn["role"].(string); role != "user" {
		t.Fatalf("tool result role = %q, want a user turn", resultTurn["role"])
	}
	resultBlocks, _ := resultTurn["content"].([]any)
	if len(resultBlocks) != 1 {
		t.Fatalf("tool result content = %v, want one tool_result block", resultTurn["content"])
	}
	result, _ := resultBlocks[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "call_1" || result["content"] != "18C" {
		t.Errorf("tool_result = %v, want the tool output carried across", result)
	}
}

func TestTranslateRequestMergesConsecutiveToolResults(t *testing.T) {
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "function": map[string]any{"name": "a", "arguments": "{}"}},
				map[string]any{"id": "call_2", "function": map[string]any{"name": "b", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "one"},
			map[string]any{"role": "tool", "tool_call_id": "call_2", "content": "two"},
		},
	}

	data, _, err := TranslateRequest(body, "claude-upstream")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	messages, _ := req["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want the two tool results merged into one user turn: %v", len(messages), messages)
	}
	blocks, _ := messages[1].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("merged content = %v, want two tool_result blocks", messages[1])
	}
}
