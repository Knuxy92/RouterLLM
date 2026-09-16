package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func boolPtr(b bool) *bool { return &b }

// baseClientBody builds a minimal chat-completions body with optional extra
// reasoning dialect keys on top.
func baseClientBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"model":    "test-model",
		"messages": []any{},
		"stream":   true,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// captureUpstreamBody starts a fake openai-style upstream that records the
// JSON body of every request and answers with a minimal SSE stream.
func captureUpstreamBody(t *testing.T) (*httptest.Server, func() map[string]any) {
	t.Helper()

	var mu sync.Mutex
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request body: %v", err)
		}
		mu.Lock()
		captured = body
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)

	return upstream, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// forwardReasoningRequest routes a chat-completions body through ForwardRaw to
// a single openai-style provider with the given reasoning style and route
// defaults, and returns the body the upstream received.
func forwardReasoningRequest(t *testing.T, reasoningStyle string, defaults model.RequestDefaults, clientBody map[string]any) map[string]any {
	t.Helper()

	upstream, captured := captureUpstreamBody(t)

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:           "test",
		BaseURL:        upstream.URL,
		Style:          "openai",
		ReasoningStyle: reasoningStyle,
		Keys:           []string{"provider-key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "test", Model: "upstream-model", Defaults: defaults}},
	}}, time.Minute)
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", req, clientBody)
	if err != nil {
		t.Fatalf("ForwardRaw: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read upstream response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upstream status = %d, body = %s", resp.StatusCode, raw)
	}

	return captured()
}

func assertMissingKeys(t *testing.T, body map[string]any, jsonBody string, keys ...string) {
	t.Helper()

	for _, k := range keys {
		if _, ok := body[k]; ok {
			t.Fatalf("upstream body should not contain key %q; body: %s", k, jsonBody)
		}
	}
}

func TestOpenAIDialectStripsLegacyThinking(t *testing.T) {
	body := forwardReasoningRequest(t, "", model.RequestDefaults{
		ReasoningEffort: "max",
		EnableThinking:  boolPtr(true),
	}, baseClientBody(map[string]any{
		"reasoning": map[string]any{"effort": "low"},
		"thinking":  map[string]any{"type": "enabled"},
	}))
	got, _ := json.Marshal(body)

	if body["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %v, want \"low\" (client dialect wins over defaults); body: %s", body["reasoning_effort"], got)
	}
	assertMissingKeys(t, body, string(got), "thinking", "enable_thinking", "reasoning", "thinking_budget")
}

func TestOpenRouterDialectMapsReasoning(t *testing.T) {
	tests := []struct {
		name       string
		defaults   model.RequestDefaults
		clientBody map[string]any
		want       map[string]any
	}{
		{
			name:     "route defaults become reasoning map",
			defaults: model.RequestDefaults{ReasoningEffort: "max", ThinkingBudget: 32000},
			want:     map[string]any{"effort": "max", "max_tokens": float64(32000)},
		},
		{
			name:       "client reasoning map wins over defaults",
			defaults:   model.RequestDefaults{ReasoningEffort: "max"},
			clientBody: map[string]any{"reasoning": map[string]any{"effort": "low", "exclude": true}},
			want:       map[string]any{"effort": "low", "exclude": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := forwardReasoningRequest(t, "openrouter", tt.defaults, baseClientBody(tt.clientBody))
			got, _ := json.Marshal(body)

			reasoning, ok := body["reasoning"].(map[string]any)
			if !ok {
				t.Fatalf("upstream body missing reasoning map; body: %s", got)
			}
			if !reflect.DeepEqual(reasoning, tt.want) {
				t.Fatalf("reasoning = %v, want %v; body: %s", reasoning, tt.want, got)
			}
			assertMissingKeys(t, body, string(got), "reasoning_effort", "enable_thinking", "thinking", "thinking_budget")
		})
	}
}

func TestQwenDialectMapsEnableThinking(t *testing.T) {
	t.Run("route defaults enable thinking", func(t *testing.T) {
		body := forwardReasoningRequest(t, "qwen", model.RequestDefaults{ReasoningEffort: "max"}, baseClientBody(nil))
		got, _ := json.Marshal(body)

		if body["enable_thinking"] != true {
			t.Fatalf("enable_thinking = %v, want true; body: %s", body["enable_thinking"], got)
		}
		assertMissingKeys(t, body, string(got), "reasoning_effort", "thinking", "reasoning", "thinking_budget")
	})

	t.Run("client enable_thinking false wins over defaults", func(t *testing.T) {
		body := forwardReasoningRequest(t, "qwen", model.RequestDefaults{ReasoningEffort: "high"},
			baseClientBody(map[string]any{"enable_thinking": false}))
		got, _ := json.Marshal(body)

		if body["enable_thinking"] != false {
			t.Fatalf("enable_thinking = %v, want false; body: %s", body["enable_thinking"], got)
		}
		assertMissingKeys(t, body, string(got), "reasoning_effort", "thinking", "reasoning", "thinking_budget")
	})
}

func TestRawReasoningStyleKeepsLegacyBody(t *testing.T) {
	body := forwardReasoningRequest(t, "raw", model.RequestDefaults{
		EnableThinking: boolPtr(true),
		ThinkingBudget: 8000,
	}, baseClientBody(nil))
	got, _ := json.Marshal(body)

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("upstream body missing legacy thinking block; body: %s", got)
	}
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(8000) {
		t.Fatalf("thinking = %v, want {type:enabled,budget_tokens:8000}; body: %s", thinking, got)
	}
	assertMissingKeys(t, body, string(got), "reasoning_effort")
}
