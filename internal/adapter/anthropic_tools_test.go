package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

const anthropicToolSSE = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_tool_1\",\"model\":\"claude-sonnet-4\",\"usage\":{\"input_tokens\":15,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Checking the weather. \"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_01\",\"name\":\"get_weather\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"location\\\":\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"San Francisco\\\"}\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":42}}\n\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestBufferAnthropicToOpenAIToolCalls(t *testing.T) {
	data, err := BufferAnthropicToOpenAI(strings.NewReader(anthropicToolSSE), "test-model")
	if err != nil {
		t.Fatalf("BufferAnthropicToOpenAI: %v", err)
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
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
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, data)
	}

	if got := resp.Choices[0].FinishReason; got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}

	msg := resp.Choices[0].Message
	if msg.Content != "Checking the weather. " {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %+v, want 1 entry", msg.ToolCalls)
	}

	tc := msg.ToolCalls[0]
	if tc.ID != "toolu_01" || tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool call = %+v", tc)
	}
	if want := `{"location":"San Francisco"}`; tc.Function.Arguments != want {
		t.Fatalf("arguments = %q, want %q", tc.Function.Arguments, want)
	}
}

func TestStreamAnthropicToOpenAIToolCalls(t *testing.T) {
	w := httptest.NewRecorder()
	if err := StreamAnthropicToOpenAI(strings.NewReader(anthropicToolSSE), w, "test-model"); err != nil {
		t.Fatalf("StreamAnthropicToOpenAI: %v", err)
	}

	body := w.Body.String()
	for _, want := range []string{
		`"delta":{"role":"assistant"}`,
		`"content":"Checking the weather. "`,
		`"id":"toolu_01"`,
		`"name":"get_weather"`,
		`"arguments":"{\"location\":"`,
		`"arguments":"\"San Francisco\"}"`,
		`"finish_reason":"tool_calls"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}

	start := strings.Index(body, `"id":"toolu_01"`)
	firstFragment := strings.Index(body, `"arguments":"{\"location\":"`)
	if start < 0 || firstFragment < 0 || start > firstFragment {
		t.Fatalf("tool start chunk must precede argument fragments:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with [DONE]:\n%s", body)
	}
}
