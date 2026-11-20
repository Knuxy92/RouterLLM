package services

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/provider"
	"routerllm/internal/workbuddy"
)

func catalogProxy(t *testing.T, pc config.ProviderConfig) *Proxy {
	t.Helper()

	registry := provider.NewRegistry([]config.ProviderConfig{pc}, nil, time.Minute)

	return NewProxy(registry, http.DefaultClient, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
}

func TestListProviderModelsCodex(t *testing.T) {
	var gotPath, gotQuery, gotAuth string

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, new(int)))
	mux.HandleFunc("/codex/models", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("client_version")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{
			map[string]any{"slug": "gpt-5.6-codex"},
			map[string]any{"slug": "gpt-5.6-codex"},
			map[string]any{"slug": "gpt-5.5"},
		}})
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	p := newCodexProxy(t, upstream, "refresh-1")
	catalog, err := p.ListProviderModels(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/codex/models" {
		t.Errorf("path = %q, want /codex/models", gotPath)
	}
	if gotQuery != "26.915.31029" {
		t.Errorf("client_version = %q", gotQuery)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("authorization = %q, want a bearer token", gotAuth)
	}
	if catalog.Style != "codex" || catalog.Source != "catalog" || catalog.Provider != "codex" {
		t.Errorf("catalog = %#v", catalog)
	}
	if len(catalog.Models) != 2 || catalog.Models[0] != "gpt-5.6-codex" || catalog.Models[1] != "gpt-5.5" {
		t.Errorf("models = %v, want deduped [gpt-5.6-codex gpt-5.5]", catalog.Models)
	}
}

func TestListProviderModelsCline(t *testing.T) {
	var gotPath, gotClientType string

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access-1",
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc("/ai/cline/recommended-models", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotClientType = r.Header.Get("X-CLIENT-TYPE")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"clinePass":  []any{map[string]any{"id": "pass-1"}},
			"free":       []any{map[string]any{"id": "free-1"}, map[string]any{"id": "pass-1"}},
			"clineCloud": []any{map[string]any{"id": "cloud-1"}},
		})
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	t.Setenv("CLINE_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "cline-accounts.json"))
	p := catalogProxy(t, config.ProviderConfig{Name: "cline", BaseURL: upstream.URL, Style: "cline", Keys: []string{"refresh-1"}})
	catalog, err := p.ListProviderModels(context.Background(), "cline")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/ai/cline/recommended-models" {
		t.Errorf("path = %q", gotPath)
	}
	if gotClientType != "cline-sdk" {
		t.Errorf("X-CLIENT-TYPE = %q, want cline-sdk", gotClientType)
	}
	if catalog.Source != "catalog" || catalog.Style != "cline" {
		t.Errorf("catalog = %#v", catalog)
	}
	want := []string{"pass-1", "free-1", "cloud-1"}
	if len(catalog.Models) != len(want) {
		t.Fatalf("models = %v, want %v", catalog.Models, want)
	}
	for i := range want {
		if catalog.Models[i] != want[i] {
			t.Fatalf("models = %v, want %v", catalog.Models, want)
		}
	}
}

func TestListProviderModelsWorkbuddy(t *testing.T) {
	seedWorkbuddyAccounts(t, "wb-refresh-1")

	var gotPath, gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc(workbuddy.RefreshPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"accessToken": "wb-access-2",
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc(workbuddy.ConfigPath, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"agents": map[string]any{"cli": map[string]any{
				"models": []string{"deepseek-v4.1-flash", "deepseek-v4.1-pro"},
			}},
		}})
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	t.Setenv("WORKBUDDY_INTL_CHAT_BASE", upstream.URL)

	p := catalogProxy(t, config.ProviderConfig{Name: "workbuddy", BaseURL: upstream.URL, Style: "workbuddy", Keys: []string{"wb-refresh-1"}})
	catalog, err := p.ListProviderModels(context.Background(), "workbuddy")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != workbuddy.ConfigPath {
		t.Errorf("path = %q, want %q", gotPath, workbuddy.ConfigPath)
	}
	if gotAuth != "Bearer wb-access-2" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if catalog.Source != "catalog" || catalog.Style != "workbuddy" {
		t.Errorf("catalog = %#v", catalog)
	}
	if len(catalog.Models) != 2 || catalog.Models[0] != "deepseek-v4.1-flash" || catalog.Models[1] != "deepseek-v4.1-pro" {
		t.Errorf("models = %v", catalog.Models)
	}
}

func TestListProviderModelsFallback(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"m1"},{"id":"m2"}]}`)
	}))
	defer upstream.Close()

	p := catalogProxy(t, config.ProviderConfig{Name: "prov", BaseURL: upstream.URL, Style: "openai", Keys: []string{"k1"}})
	catalog, err := p.ListProviderModels(context.Background(), "prov")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/models" {
		t.Errorf("path = %q, want /models", gotPath)
	}
	if gotAuth != "Bearer k1" {
		t.Errorf("authorization = %q, want Bearer k1", gotAuth)
	}
	if catalog.Source != "fallback" || catalog.Style != "openai" {
		t.Errorf("catalog = %#v", catalog)
	}
	if len(catalog.Models) != 2 || catalog.Models[0] != "m1" || catalog.Models[1] != "m2" {
		t.Errorf("models = %v", catalog.Models)
	}
}

func TestListProviderModelsFallbackNoAuthMode(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"m1"}]}`)
	}))
	defer upstream.Close()

	p := catalogProxy(t, config.ProviderConfig{Name: "prov", BaseURL: upstream.URL, Style: "openai", AuthMode: "none"})
	catalog, err := p.ListProviderModels(context.Background(), "prov")
	if err != nil {
		t.Fatalf("auth_mode none with no keys should succeed: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("authorization = %q, want none", gotAuth)
	}
	if len(catalog.Models) != 1 || catalog.Models[0] != "m1" {
		t.Errorf("models = %v", catalog.Models)
	}
}

func TestListProviderModelsUnknownProvider(t *testing.T) {
	p := catalogProxy(t, config.ProviderConfig{Name: "prov", BaseURL: "http://example.test", Style: "openai", Keys: []string{"k1"}})
	_, err := p.ListProviderModels(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %q, want to name the provider", err)
	}
}

func TestListProviderModelsNoKeys(t *testing.T) {
	p := catalogProxy(t, config.ProviderConfig{Name: "prov", BaseURL: "http://example.test", Style: "openai"})
	_, err := p.ListProviderModels(context.Background(), "prov")
	if err == nil || !strings.Contains(err.Error(), "no available keys") {
		t.Fatalf("error = %v, want no available keys", err)
	}
}

func TestListProviderModelsNon200RedactsBody(t *testing.T) {
	const secret = "SUPER_SECRET_UPSTREAM_BODY"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, secret)
	}))
	defer upstream.Close()

	p := catalogProxy(t, config.ProviderConfig{Name: "prov", BaseURL: upstream.URL, Style: "openai", Keys: []string{"k1"}})
	_, err := p.ListProviderModels(context.Background(), "prov")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks upstream body: %q", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want status code", err)
	}
}
