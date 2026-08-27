package services

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func TestForwardRawClientHeaders(t *testing.T) {
	tests := []struct {
		name    string
		forward bool
		allow   []string
		want    string
	}{
		{name: "enabled", forward: true, want: "client-value"},
		{name: "disabled", forward: false},
		{name: "allowlist", forward: true, allow: []string{"X-Other-Header"}, want: ""},
		{name: "allowlist matches", forward: true, allow: []string{"x-client-header"}, want: "client-value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Client-Header"); got != tt.want {
					t.Errorf("X-Client-Header = %q, want %q", got, tt.want)
				}
				if got := r.Header.Get("X-Provider-Header"); got != "provider-value" {
					t.Errorf("X-Provider-Header = %q, want provider-value", got)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer provider-key" {
					t.Errorf("Authorization = %q, want provider key", got)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"test"}`)
			}))
			defer upstream.Close()

			registry := provider.NewRegistry([]config.ProviderConfig{{
				Name:    "test",
				BaseURL: upstream.URL,
				Style:   "openai",
				Keys:    []string{"provider-key"},
				Headers: map[string]string{"X-Provider-Header": "provider-value"},
			}}, []model.Rule{{
				ModelID: "test-model",
				Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
			}}, time.Minute)
			proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, tt.forward, tt.allow, "")
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("X-Client-Header", "client-value")
			req.Header.Set("Authorization", "Bearer client-token")

			resp, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
				"model":    "test-model",
				"messages": []any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
	}
}
