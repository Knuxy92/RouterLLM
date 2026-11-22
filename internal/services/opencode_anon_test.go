package services

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/opencode"
	"routerllm/internal/provider"
)

// opencodeAnonRegistry builds a style-opencode registry in the given
// opencode_auth mode. Keys are the account refresh tokens (empty for
// anonymous-only setups).
func opencodeAnonRegistry(upstream *httptest.Server, authMode string, keys ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "oc-anon", BaseURL: upstream.URL, Style: "opencode",
		OpencodeAuth: authMode, Keys: keys,
	}}, []model.Rule{{
		ModelID: "oc-anon/m", Routes: []model.Spec{{Provider: "oc-anon", Model: "up-model"}},
	}}, time.Minute)
}

// newOpencodeAnonProxy adds the account store and manager bolt needs for the
// login leg, mirroring newOpencodeTestProxy.
func newOpencodeAnonProxy(t *testing.T, upstream *httptest.Server, registry *provider.Registry) *Proxy {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "opencode-accounts.json")
	t.Setenv("OPENCODE_ACCOUNTS_FILE", storePath)
	store, err := opencode.LoadAccountStore(storePath)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}
	if err := store.Add(opencode.Account{
		AccountID: "u-1", Email: "dev@example.test", RefreshToken: "oc_refresh_token",
		Server: upstream.URL, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.opencodeMu.Lock()
	proxy.opencodeManagers[upstream.URL] = opencode.NewManager(&opencode.Client{
		HTTPClient: upstream.Client(), Server: upstream.URL,
	}, store)
	proxy.opencodeMu.Unlock()

	return proxy
}

// assertAnonOpenCodeHeaders enforces the identity set with the signed-out
// placeholder credential instead of a Console access token.
func assertAnonOpenCodeHeaders(t *testing.T, r *http.Request) {
	t.Helper()

	assertOpenCodeIdentity(t, r)
	if got := r.Header.Get("Authorization"); got != "Bearer "+opencodeAnonKey {
		t.Errorf("Authorization = %q, want Bearer %s", got, opencodeAnonKey)
	}
}

func anonChatBody() map[string]any {
	return map[string]any{
		"model":    "oc-anon/m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
}

func TestForwardOpencodeAnonymousSendsPublicKey(t *testing.T) {
	tokenExchangeHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			tokenExchangeHits++
			t.Errorf("anonymous mode must never hit the Console token exchange")
			return
		}
		assertAnonOpenCodeHeaders(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(opencodeAnonRegistry(upstream, "anonymous"), upstream.Client(),
		log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions",
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")),
		anonChatBody())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if tokenExchangeHits != 0 {
		t.Fatalf("token exchange hit %d times, want 0", tokenExchangeHits)
	}
}

func TestForwardOpencodeBoltFallsBackToLogin(t *testing.T) {
	anonHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			opencodeTokenExchange(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+opencodeAnonKey {
			anonHits++
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached. Free models work in OpenCode"}}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oc_access_token" {
			t.Errorf("login attempt Authorization = %q, want Bearer oc_access_token", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeAnonProxy(t, upstream, opencodeAnonRegistry(upstream, "bolt", "oc_refresh_token"))
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions",
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")),
		anonChatBody())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 via the login leg", resp.StatusCode)
	}
	if anonHits != 1 {
		t.Fatalf("anonymous hits = %d, want 1", anonHits)
	}
	if proxy.anonGateFor("oc-anon").allowed(time.Now()) {
		t.Fatal("gate must be tripped after the anonymous 429")
	}

	// Second request inside the cooldown: anonymous is skipped entirely and
	// the login pool serves directly.
	resp2, _, _, err := proxy.ForwardRaw("/v1/chat/completions",
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")),
		anonChatBody())
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200", resp2.StatusCode)
	}
	if anonHits != 1 {
		t.Fatalf("anonymous hits = %d after the trip, want it frozen at 1", anonHits)
	}
}

func TestForwardOpencodeAnonymousOnlySurfaces429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			t.Errorf("anonymous-only mode must never hit the Console token exchange")
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached"}}`)
	}))
	defer upstream.Close()

	proxy := NewProxy(opencodeAnonRegistry(upstream, "anonymous"), upstream.Client(),
		log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions",
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")),
		anonChatBody())
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal("forward unexpectedly succeeded on a persistent anonymous 429")
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if proxy.anonGateFor("oc-anon").allowed(time.Now()) {
		t.Fatal("gate must be tripped after the anonymous 429")
	}
}

func TestAnonGateReopensAfterCooldown(t *testing.T) {
	var gate anonGate
	now := time.Now()
	if !gate.allowed(now) {
		t.Fatal("fresh gate must allow")
	}

	gate.trip(now, 30*time.Millisecond)
	if gate.allowed(now) {
		t.Fatal("tripped gate must block")
	}
	if !gate.allowed(now.Add(40 * time.Millisecond)) {
		t.Fatal("gate must reopen after the cooldown")
	}
}

func TestListProviderModelsAnonymousUsesPublicKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+opencodeAnonKey {
			t.Errorf("catalog Authorization = %q, want Bearer %s", got, opencodeAnonKey)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m-free"},{"id":"m-paid"}]}`)
	}))
	defer upstream.Close()

	proxy := NewProxy(opencodeAnonRegistry(upstream, "anonymous"), upstream.Client(),
		log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	catalog, err := proxy.ListProviderModels(context.Background(), "oc-anon")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 || catalog.Models[0] != "m-free" {
		t.Fatalf("models = %#v, want [m-free m-paid]", catalog.Models)
	}
}
