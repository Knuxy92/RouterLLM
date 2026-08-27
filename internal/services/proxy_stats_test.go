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

func TestProviderStatsCountRequestsAndErrors(t *testing.T) {
	tests := []struct {
		name         string
		upstreamCode int
		wantErr      bool
		wantRequests uint64
		wantErrors   uint64
	}{
		{name: "success", upstreamCode: http.StatusOK, wantRequests: 1, wantErrors: 0},
		{name: "non-200 handed back to caller", upstreamCode: http.StatusBadRequest, wantErr: true, wantRequests: 1, wantErrors: 1},
		{name: "key marked dead exhausts provider", upstreamCode: http.StatusUnauthorized, wantErr: true, wantRequests: 1, wantErrors: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.upstreamCode)
				io.WriteString(w, `{"id":"test"}`)
			}))
			defer upstream.Close()

			registry := provider.NewRegistry([]config.ProviderConfig{{
				Name:    "test",
				BaseURL: upstream.URL,
				Style:   "openai",
				Keys:    []string{"provider-key"},
			}}, []model.Rule{{
				ModelID: "test-model",
				Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
			}}, time.Minute)
			proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			resp, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
				"model":    "test-model",
				"messages": []any{},
			})
			if resp != nil {
				resp.Body.Close()
			}
			if tt.wantErr && err == nil {
				t.Fatalf("ForwardRaw() error = nil, want failure for upstream %d", tt.upstreamCode)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ForwardRaw() error = %v", err)
			}

			pv, ok := registry.Provider("test")
			if !ok {
				t.Fatal("provider test missing from registry")
			}
			if got := pv.Stats().Requests(); got != tt.wantRequests {
				t.Errorf("Requests() = %d, want %d", got, tt.wantRequests)
			}
			if got := pv.Stats().Errors(); got != tt.wantErrors {
				t.Errorf("Errors() = %d, want %d", got, tt.wantErrors)
			}
		})
	}
}
