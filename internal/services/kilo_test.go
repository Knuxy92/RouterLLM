package services

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/kilo"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func kiloRegistry(baseURL string, tokens ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "kilo", BaseURL: baseURL, Style: "kilo", Keys: tokens, AuthMode: "bearer",
	}}, []model.Rule{{
		ModelID: "kilo/auto", Routes: []model.Spec{{Provider: "kilo", Model: "openrouter/auto"}},
	}}, time.Minute)
}

func TestForwardRawSendsKiloTokenAndOrg(t *testing.T) {
	accounts := filepath.Join(t.TempDir(), "kilo-accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"accounts":[{"accountId":"acc_1","token":"tok_live","organizationId":"org-1","createdAt":"2026-01-01T00:00:00Z"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_ACCOUNTS_FILE", accounts)

	var gotHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(kiloRegistry(upstream.URL, "tok_live"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
	req.Header.Set(kilo.OrgHeader, "client-forged-org")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
		"model":    "kilo/auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := gotHeader.Get("Authorization"); got != "Bearer tok_live" {
		t.Fatalf("authorization = %q, want Bearer tok_live", got)
	}
	if got := gotHeader.Get(kilo.OrgHeader); got != "org-1" {
		t.Fatalf("%s = %q, want org-1", kilo.OrgHeader, got)
	}
}

func TestKiloModelsFetchesCatalogWithOrg(t *testing.T) {
	accounts := filepath.Join(t.TempDir(), "kilo-accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"accounts":[{"accountId":"acc_1","token":"tok_live","organizationId":"org-1","createdAt":"2026-01-01T00:00:00Z"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_ACCOUNTS_FILE", accounts)

	var gotPath, gotOrg string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotOrg = r.Header.Get(kilo.OrgHeader)
		_, _ = io.WriteString(w, `{"data":[{"id":"kilo-fast"},{"id":"kilo-max"}]}`)
	}))
	defer upstream.Close()

	pv := &provider.Provider{
		Name: "kilo", BaseURL: upstream.URL, Style: "kilo", AuthMode: "bearer",
	}
	proxy := NewProxy(kiloRegistry(upstream.URL, "tok_live"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	source, models, err := proxy.kiloModels(t.Context(), pv, "tok_live")
	if err != nil {
		t.Fatal(err)
	}
	if source != "catalog" {
		t.Fatalf("source = %q, want catalog", source)
	}
	if len(models) != 2 || models[0] != "kilo-fast" || models[1] != "kilo-max" {
		t.Fatalf("models = %#v", models)
	}
	if !strings.HasSuffix(gotPath, "/organizations/org-1/models") {
		t.Fatalf("catalog path = %q, want org-scoped models", gotPath)
	}
	if gotOrg != "org-1" {
		t.Fatalf("org header = %q, want org-1", gotOrg)
	}
}
