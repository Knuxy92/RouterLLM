package adapter

import (
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
