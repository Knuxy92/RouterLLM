package handlers

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
	"routerllm/internal/services"
)

func TestMessagesHandlerStyleCallRawPassthrough(t *testing.T) {
	const anthropicSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if _, ok := body["messages"]; !ok {
			t.Errorf("anthropic-shaped body missing messages: %v", body)
		}
		if _, ok := body["input"]; ok {
			t.Errorf("Responses input leaked into anthropic body: %v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, anthropicSSE)
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "test", BaseURL: upstream.URL, Style: "openai", Keys: []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model", Routes: []model.Spec{{Provider: "test", Model: "claude-upstream", StyleCall: "messages"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, true, true, nil, "")
	h := New(proxy)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`,
	))
	w := httptest.NewRecorder()

	h.Messages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}
	if w.Body.String() != anthropicSSE {
		t.Fatalf("body was translated:\n%s", w.Body.String())
	}
}
