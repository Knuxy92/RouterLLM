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

const longToolName = "mcp__plugin_chrome-devtools-mcp_chrome-devtools__get_console_message"

func toolNameTestRegistry(upstream *httptest.Server, spec model.Spec) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "openai-test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{spec},
	}}, time.Minute)
}

func toolCallUpstream(t *testing.T, wantName string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}

		tools, _ := body["tools"].([]any)
		if len(tools) == 0 {
			t.Errorf("tools missing upstream: %v", body)
		} else if name := tools[0].(map[string]any)["function"].(map[string]any)["name"]; name != wantName {
			t.Errorf("upstream tool name = %q, want %q", name, wantName)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"up\","+
			"\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\","+
			"\"function\":{\"name\":\""+wantName+"\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
}

func TestForwardSanitizesLongToolNames(t *testing.T) {
	sanitized := sanitizeToolName(longToolName)
	if len(sanitized) > 64 {
		t.Fatalf("sanitized name is %d chars, must fit the 64-char cap", len(sanitized))
	}

	upstream := toolCallUpstream(t, sanitized)
	defer upstream.Close()

	registry := toolNameTestRegistry(upstream, model.Spec{
		Provider:          "openai-test",
		Model:             "gpt-upstream",
		SanitizeToolNames: true,
	})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],`+
			`"tools":[{"type":"function","function":{"name":"`+longToolName+`","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, longToolName) {
		t.Fatalf("client must get the original tool name back:\n%s", body)
	}
	if strings.Contains(body, sanitized) {
		t.Fatalf("client must not see the sanitized name:\n%s", body)
	}
}

func TestForwardSanitizeOffKeepsNames(t *testing.T) {
	upstream := toolCallUpstream(t, longToolName)
	defer upstream.Close()

	registry := toolNameTestRegistry(upstream, model.Spec{Provider: "openai-test", Model: "gpt-upstream"})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],`+
			`"tools":[{"type":"function","function":{"name":"`+longToolName+`","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), longToolName) {
		t.Fatalf("names must pass through untouched when the toggle is off:\n%s", w.Body.String())
	}
}

func TestForwardDedupesTools(t *testing.T) {
	var seen int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		tools, _ := body["tools"].([]any)
		seen = len(tools)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	registry := toolNameTestRegistry(upstream, model.Spec{
		Provider:    "openai-test",
		Model:       "gpt-upstream",
		DedupeTools: true,
	})
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],"tools":[`+
			`{"type":"function","function":{"name":"dup","parameters":{"type":"object"}}},`+
			`{"type":"function","function":{"name":"dup","parameters":{"type":"object"}}},`+
			`{"type":"function","function":{"name":"other","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if seen != 2 {
		t.Fatalf("upstream saw %d tools, want 2 (duplicate dropped)", seen)
	}
}
