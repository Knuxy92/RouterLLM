package handlers

import (
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
		Model: "test-model", Routes: []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
	proxy := services.NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, true, "")
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
