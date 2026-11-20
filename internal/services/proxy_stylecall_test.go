package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

const anthropicUpstreamSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"claude-upstream\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi from claude\"}}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

const responsesUpstreamSSE = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"created_at\":1700000000}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7}}}\n\n"

func newStyleCallRegistry(upstream *httptest.Server, rules []model.Rule) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "openai-test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"key"},
	}}, rules, time.Minute)
}

func TestForwardStyleCallMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if body["system"] != "be nice" {
			t.Errorf("system = %v, want be nice", body["system"])
		}
		if msgs, ok := body["messages"].([]any); !ok || len(msgs) == 0 {
			t.Errorf("messages missing: %v", body)
		}
		if _, ok := body["max_tokens"]; !ok {
			t.Errorf("max_tokens missing: %v", body)
		}
		if _, ok := body["input"]; ok {
			t.Errorf("Responses input leaked into anthropic body: %v", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, anthropicUpstreamSSE)
	}))
	defer upstream.Close()

	registry := newStyleCallRegistry(upstream, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "openai-test", Model: "claude-upstream", StyleCall: "messages"}},
	}})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hello"}],"stream":true}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"hi from claude"`) {
		t.Fatalf("anthropic delta not converted to chat chunk:\n%s", body)
	}
	if !strings.Contains(body, `"object":"chat.completion.chunk"`) {
		t.Fatalf("chunks are not OpenAI-shaped:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("must end with [DONE]:\n%s", body)
	}
}

func TestForwardStyleCallResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q, want /v1/responses", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if _, ok := body["input"]; !ok {
			t.Errorf("input missing: %v", body)
		}
		if got, _ := body["max_output_tokens"].(float64); got != 256 {
			t.Errorf("max_output_tokens = %v, want 256", body["max_output_tokens"])
		}
		if body["stream"] != true {
			t.Errorf("stream must be forced true: %v", body)
		}
		if _, ok := body["stream_options"]; ok {
			t.Errorf("stream_options must not leak into responses body: %v", body)
		}
		if _, ok := body["reasoning"]; ok {
			t.Errorf("reasoning must not appear when not requested: %v", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsesUpstreamSSE)
	}))
	defer upstream.Close()

	registry := newStyleCallRegistry(upstream, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "openai-test", Model: "gpt-upstream", StyleCall: "responses"}},
	}})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	t.Run("stream client", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
			`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":256,"stream":true}`))
		w := httptest.NewRecorder()

		proxy.Forward("/v1/chat/completions", w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, `"content":"Hello "`) || !strings.Contains(body, `"content":"world"`) {
			t.Fatalf("text deltas missing:\n%s", body)
		}
		if !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("[DONE] missing:\n%s", body)
		}
	})

	t.Run("non-stream client", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
			`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":256,"stream":false}`))
		w := httptest.NewRecorder()

		proxy.Forward("/v1/chat/completions", w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var resp model.ChatCompletionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not a single JSON response: %v\n%s", err, w.Body.String())
		}
		if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello world" {
			t.Fatalf("choices = %+v", resp.Choices)
		}
		if resp.Model != "gpt-upstream" {
			t.Fatalf("model = %q, want gpt-upstream", resp.Model)
		}
		if resp.Choices[0].FinishReason != "stop" {
			t.Fatalf("finish_reason = %q", resp.Choices[0].FinishReason)
		}
		if resp.Usage == nil || !strings.Contains(string(resp.Usage), `"completion_tokens":4`) {
			t.Fatalf("usage = %s", resp.Usage)
		}
	})
}

func TestResponsesInboundFiltering(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q, want /v1/responses", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if body["model"] != "up-resp" {
			t.Errorf("model = %v, want up-resp", body["model"])
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("input not preserved: %v", body)
		}
		item, _ := input[0].(map[string]any)
		if item["role"] != "user" || item["content"] != "hi" {
			t.Errorf("input item = %v, want user/hi", item)
		}
		if body["stream"] != false {
			t.Errorf("stream = %v, want false (client asked for non-stream)", body["stream"])
		}

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp-9","status":"completed","output":[]}`)
	}))
	defer upstream.Close()

	registry := newStyleCallRegistry(upstream, []model.Rule{
		{
			ModelID: "chat-only",
			Routes:  []model.Spec{{Provider: "openai-test", Model: "up-chat", StyleCall: "chat"}},
		},
		{
			ModelID: "responses-only",
			Routes:  []model.Spec{{Provider: "openai-test", Model: "up-resp", StyleCall: "responses"}},
		},
	})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	t.Run("chat dialect leg is skipped", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp, _, _, err := proxy.ForwardRaw("/v1/responses", req, map[string]any{
			"model": "chat-only",
			"input": "hi",
		})
		if err == nil {
			if resp != nil {
				resp.Body.Close()
			}
			t.Fatal("expected error for chat-only model on /v1/responses")
		}
		if !strings.Contains(err.Error(), "not found for /v1/responses") {
			t.Fatalf("error = %v, want not found for /v1/responses", err)
		}
		if got := hits.Load(); got != 0 {
			t.Fatalf("upstream hit %d times, want 0", got)
		}
	})

	t.Run("responses dialect leg is a passthrough", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
			`{"model":"responses-only","input":[{"role":"user","content":"hi"}],"stream":false}`))
		w := httptest.NewRecorder()

		proxy.Forward("/v1/responses", w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		if body := w.Body.String(); body != `{"id":"resp-9","status":"completed","output":[]}` {
			t.Fatalf("raw response not copied through: %s", body)
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("upstream hits = %d, want 1", got)
		}
	})
}

func TestForceStreamStyleCallResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsesUpstreamSSE)
	}))
	defer upstream.Close()

	registry := newStyleCallRegistry(upstream, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "openai-test", Model: "gpt-upstream", StyleCall: "responses"}},
	}})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, true, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Fatalf("message_start missing:\n%s", body)
	}
	if !strings.Contains(body, `"text":"Hello ","type":"text_delta"`) || !strings.Contains(body, `"text":"world","type":"text_delta"`) {
		t.Fatalf("text deltas missing:\n%s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop missing:\n%s", body)
	}
}
