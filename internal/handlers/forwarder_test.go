package handlers

import (
	"bytes"
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
	"routerllm/internal/services"
)

func TestMessagesDoesNotDoubleConvertForceStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "test", BaseURL: upstream.URL, Style: "openai", Keys: []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model", Routes: []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, true, "", config.AutoModelConfig{})
	h := New(proxy, registry.AllModels())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`,
	))
	w := httptest.NewRecorder()

	h.Messages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Count(body, "event: message_start") != 1 {
		t.Fatalf("message_start count != 1:\n%s", body)
	}
	if !strings.Contains(body, `"text":"hello","type":"text_delta"`) {
		t.Fatalf("missing translated text delta:\n%s", body)
	}
}

func TestMessagesPassesThroughAnthropicRoute(t *testing.T) {
	const anthropicSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, anthropicSSE)
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "test", BaseURL: upstream.URL, Style: "anthropic", Keys: []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model", Routes: []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, true, "", config.AutoModelConfig{})
	h := New(proxy, registry.AllModels())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`,
	))
	w := httptest.NewRecorder()

	h.Messages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != anthropicSSE {
		t.Fatalf("body was translated:\n%s", w.Body.String())
	}
}

func TestChatCompletionsRejectsJSONNull(t *testing.T) {
	proxy := services.NewProxy(nil, nil, log.New(io.Discard, "", 0), false, false, false, "", config.AutoModelConfig{})
	h := New(proxy, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("null"))
	w := httptest.NewRecorder()

	h.ChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("unexpected error body: %s", w.Body.String())
	}
}

func TestMessagesNormalizesUpstreamErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`)
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "test", BaseURL: upstream.URL, Style: "openai", Keys: []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model", Routes: []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, true, "", config.AutoModelConfig{})
	h := New(proxy, registry.AllModels())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`,
	))
	w := httptest.NewRecorder()

	h.Messages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"upstream_error"`) || !strings.Contains(w.Body.String(), `"message":"bad request"`) {
		t.Fatalf("unexpected error body: %s", w.Body.String())
	}
}

func TestFilesRequiresModelQuery(t *testing.T) {
	proxy := services.NewProxy(nil, nil, log.New(io.Discard, "", 0), false, false, false, "", config.AutoModelConfig{})
	h := New(proxy, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	w := httptest.NewRecorder()

	h.Files(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestFilesPassesThroughSelectedOpenAIProvider(t *testing.T) {
	const payload = `{"id":"file-1","object":"file"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/files" {
			t.Errorf("request = %s %s, want POST /v1/files", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("authorization = %q, want Bearer key", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, []byte("file-bytes")) {
			t.Errorf("body = %q, want file-bytes", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, payload)
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "test", BaseURL: upstream.URL, Style: "openai", Keys: []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model", Routes: []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, "", config.AutoModelConfig{})
	h := New(proxy, registry.AllModels())
	req := httptest.NewRequest(http.MethodPost, "/v1/files?model=test-model", strings.NewReader("file-bytes"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	w := httptest.NewRecorder()

	h.Files(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != payload {
		t.Fatalf("body = %q, want %q", w.Body.String(), payload)
	}
}
