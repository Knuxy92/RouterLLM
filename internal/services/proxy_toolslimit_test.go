package services

import (
	"encoding/json"
	"errors"
	"fmt"
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

// alysisToolProxy builds a single-provider proxy over a fake upstream for
// tool-limit tests. The provider style decides whether the guard applies.
func alysisToolProxy(t *testing.T, style string) (*Proxy, func() map[string]any) {
	t.Helper()

	upstream, captured := captureUpstreamBody(t)
	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "test",
		BaseURL: upstream.URL,
		Style:   style,
		Keys:    []string{"provider-key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)

	return NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, ""), captured
}

func chatTools(n int) []any {
	tools := make([]any, n)
	for i := range tools {
		tools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       fmt.Sprintf("tool_%03d", i),
				"parameters": map[string]any{"type": "object"},
			},
		}
	}

	return tools
}

func TestAlysisRejectsRequestsOverToolLimit(t *testing.T) {
	proxy, captured := alysisToolProxy(t, "alysis")

	body := baseClientBody(map[string]any{"tools": chatTools(129)})
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if captured() != nil {
		t.Fatalf("upstream must not be called for requests over the tool limit")
	}
	if !strings.Contains(rec.Body.String(), "too many tools") || !strings.Contains(rec.Body.String(), "129") {
		t.Fatalf("error should name the limit and the tool count; body: %s", rec.Body.String())
	}
}

func TestAlysisAllowsRequestsAtToolLimit(t *testing.T) {
	proxy, captured := alysisToolProxy(t, "alysis")

	_, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), baseClientBody(map[string]any{"tools": chatTools(128)}))
	if err != nil {
		t.Fatalf("ForwardRaw: %v", err)
	}

	upstreamBody := captured()
	if upstreamBody == nil {
		t.Fatalf("upstream was not called")
	}
	tools, _ := upstreamBody["tools"].([]any)
	if len(tools) != 128 {
		t.Fatalf("upstream tools = %d, want 128", len(tools))
	}
}

func TestAlysisToolLimitErrorIsSentinel(t *testing.T) {
	proxy, _ := alysisToolProxy(t, "alysis")

	_, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), baseClientBody(map[string]any{"tools": chatTools(131)}))
	if !errors.Is(err, errTooManyTools) {
		t.Fatalf("err = %v, want errTooManyTools", err)
	}
}

func TestOpenAIStyleHasNoToolLimit(t *testing.T) {
	proxy, captured := alysisToolProxy(t, "openai")

	_, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), baseClientBody(map[string]any{"tools": chatTools(129)}))
	if err != nil {
		t.Fatalf("ForwardRaw: %v", err)
	}
	if captured() == nil {
		t.Fatalf("openai-style providers must pass large tool sets through")
	}
}
