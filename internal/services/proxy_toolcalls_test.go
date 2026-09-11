package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

// toolCallUpstreamSSE streams one tool call whose arguments arrive in two
// fragments after the identity fields.
const toolCallUpstreamSSE = "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Paris\\\"}\"}}]}}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
	"data: [DONE]\n\n"

func newToolCallProxy(t *testing.T, sse string) *Proxy {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	t.Cleanup(upstream.Close)

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"provider-key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)

	return NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
}

// TestNonStreamingClientBuffersToolCalls drives the full buffering path a
// non-streaming client takes (Forward -> ForwardRaw -> serveOpenAI ->
// bufferStream) and asserts streamed tool_call fragments come back assembled.
func TestNonStreamingClientBuffersToolCalls(t *testing.T) {
	proxy := newToolCallProxy(t, toolCallUpstreamSSE)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"weather?"}],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("client body is not a single JSON response: %v\n%s", err, w.Body.String())
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}

	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %+v, want 1 assembled call", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Type != "function" || calls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_call identity = %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("arguments = %q, want the concatenated fragments", calls[0].Function.Arguments)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}

	var raw struct {
		Choices []struct {
			Message struct {
				ToolCalls []map[string]any `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Choices[0].Message.ToolCalls[0]["index"]; !ok {
		t.Fatalf("serialized tool_call lost its index: %v", raw.Choices[0].Message.ToolCalls[0])
	}
}

func TestBufferStreamOrdersToolCallsByIndex(t *testing.T) {
	const sse = "data: {\"id\":\"c2\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_b\",\"type\":\"function\",\"function\":{\"name\":\"b\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"id\":\"c2\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"type\":\"function\",\"function\":{\"name\":\"a\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	result := bufferStream(strings.NewReader(sse))

	if len(result.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(result.Choices))
	}
	calls := result.Choices[0].Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("tool_calls = %+v, want 2", calls)
	}
	if calls[0].ID != "call_a" || calls[1].ID != "call_b" {
		t.Fatalf("tool_calls not ordered by index: %+v", calls)
	}
}
