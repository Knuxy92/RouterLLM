package services

import (
	"encoding/json"
	"fmt"
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

func opencodeRegistry(baseURL string, refreshTokens ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "zen", BaseURL: baseURL, Style: "opencode", Keys: refreshTokens,
	}}, []model.Rule{{
		ModelID: "zen-test", Routes: []model.Spec{{Provider: "zen", Model: "some-model"}},
	}}, time.Minute)
}

// newOpencodeProxy wires the fake upstream as both the Console server and the
// inference gateway: the account store holds one account whose server is the
// fake URL, and the manager for that server is pre-injected so no request
// escapes to the real Console.
func newOpencodeProxy(t *testing.T, upstream *httptest.Server, withOrg bool) *Proxy {
	t.Helper()

	account := opencode.Account{
		AccountID: "u-1", Email: "dev@example.test", RefreshToken: "refresh-1",
		Server: upstream.URL, CreatedAt: time.Now(),
	}
	if withOrg {
		account.OrgID, account.OrgName = "org-1", "Acme"
	}

	storePath := filepath.Join(t.TempDir(), "opencode-accounts.json")
	t.Setenv("OPENCODE_ACCOUNTS_FILE", storePath)
	store, err := opencode.LoadAccountStore(storePath)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}
	if err := store.Add(account); err != nil {
		t.Fatalf("Add: %v", err)
	}

	proxy := NewProxy(opencodeRegistry(upstream.URL, "refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.opencodeMu.Lock()
	proxy.opencodeManagers[upstream.URL] = opencode.NewManager(&opencode.Client{
		HTTPClient: upstream.Client(), Server: upstream.URL,
	}, store)
	proxy.opencodeMu.Unlock()

	return proxy
}

func opencodeTokenHandler(t *testing.T, refreshes *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode token request: %v", err)
		}
		if body["grant_type"] != "refresh_token" || body["refresh_token"] == "" || body["client_id"] != "opencode-cli" {
			t.Errorf("token body = %v", body)
		}

		(*refreshes)++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-1",
			"expires_in":    0,
		})
	}
}

// opencodeSequencedTokenHandler mints one distinct access token per exchange
// ("access-1", "access-2", ...) so stale-401 tests can prove the retry went
// out with the refreshed token, not the cached one.
func opencodeSequencedTokenHandler(t *testing.T, exchanges *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode token request: %v", err)
		}
		if body["grant_type"] != "refresh_token" || body["refresh_token"] == "" || body["client_id"] != "opencode-cli" {
			t.Errorf("token body = %v", body)
		}

		(*exchanges)++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fmt.Sprintf("access-%d", *exchanges),
			"refresh_token": "refresh-1",
			"expires_in":    0,
		})
	}
}

const opencodeChatSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"hello opencode\"},\"finish_reason\":\"stop\",\"index\":0}],\"created\":1,\"id\":\"chatcmpl-1\",\"model\":\"some-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
	"data: [DONE]\n\n"

func TestForwardOpenCodeUsesAccountTokenNotPoolKey(t *testing.T) {
	refreshes := 0
	var auth, orgID string

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device/token", opencodeTokenHandler(t, &refreshes))
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		orgID = r.Header.Get("X-Org-Id")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, opencodeChatSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newOpencodeProxy(t, upstream, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"zen-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if auth != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want the minted access token, never the pool key", auth)
	}
	if orgID != "org-1" {
		t.Fatalf("X-Org-Id = %q, want org-1 from the serving account", orgID)
	}
	if refreshes < 1 {
		t.Fatalf("refreshes = %d, want at least one exchange", refreshes)
	}
}

func TestForwardOpenCodeOmitsOrgHeaderWithoutOrg(t *testing.T) {
	refreshes := 0
	var orgID string
	var orgSeen bool

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device/token", opencodeTokenHandler(t, &refreshes))
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_, orgSeen = r.Header["X-Org-Id"]
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, opencodeChatSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newOpencodeProxy(t, upstream, false)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"zen-test","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if orgSeen {
		t.Fatalf("X-Org-Id sent without an org: %q", orgID)
	}
}

func TestForwardRawRefreshesOpencodeTokenOnUnauthorized(t *testing.T) {
	refreshes := 0
	attempts := 0
	var authHeaders []string

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device/token", opencodeSequencedTokenHandler(t, &refreshes))
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_token","message":"expired"}}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, opencodeChatSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newOpencodeProxy(t, upstream, false)
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")), map[string]any{
		"model":    "zen-test",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(authHeaders) != 2 || authHeaders[1] == authHeaders[0] {
		t.Fatalf("authorization headers = %#v, want a refreshed token on retry", authHeaders)
	}
}

func TestPruneOpencodeManagers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	proxy := newOpencodeProxy(t, upstream, false)
	if len(proxy.opencodeManagers) != 1 {
		t.Fatalf("managers = %d, want 1", len(proxy.opencodeManagers))
	}

	// Same registry shape still configures an opencode provider: kept.
	proxy.Apply(opencodeRegistry(upstream.URL, "refresh-1"), "")
	if len(proxy.opencodeManagers) != 1 {
		t.Fatalf("managers after reload with opencode = %d, want 1", len(proxy.opencodeManagers))
	}

	// No opencode provider left: dropped.
	empty := provider.NewRegistry(nil, nil, time.Minute)
	proxy.Apply(empty, "")
	if len(proxy.opencodeManagers) != 0 {
		t.Fatalf("managers after reload without opencode = %d, want 0", len(proxy.opencodeManagers))
	}
}
