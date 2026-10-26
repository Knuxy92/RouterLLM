package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/workbuddy"
)

func workbuddyRegistry(baseURL string, refreshTokens ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "workbuddy", BaseURL: baseURL, Style: "workbuddy", Keys: refreshTokens,
	}}, []model.Rule{{
		ModelID: "wb/deepseek-v4.1-flash", Routes: []model.Spec{{Provider: "workbuddy", Model: "deepseek-v4.1-flash"}},
	}}, time.Minute)
}

func workbuddyRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
}

// seedWorkbuddyAccounts writes an account store holding one intl desktop
// account and points WORKBUDDY_ACCOUNTS_FILE at it.
func seedWorkbuddyAccounts(t *testing.T, refreshToken string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "wb-accounts.json")
	store, err := workbuddy.LoadAccountStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(workbuddy.Account{
		UID: "uid-1", RefreshToken: refreshToken, Realm: "intl", Product: "desktop", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKBUDDY_ACCOUNTS_FILE", path)
}

// workbuddyUpstream stubs the realm host: the token refresh endpoint and the
// chat endpoint. chat receives the 1-based call number for the chat endpoint.
func workbuddyUpstream(t *testing.T, chat func(w http.ResponseWriter, r *http.Request, call int)) *httptest.Server {
	t.Helper()

	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc(workbuddy.RefreshPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Id") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":401,"msg":"unknown account"}`)
			return
		}
		if got := r.Header.Get("X-Refresh-Token"); got == "" {
			t.Errorf("refresh without X-Refresh-Token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"accessToken":  "wb-access-2",
			"refreshToken": "wb-refresh-2",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc(workbuddy.ChatPath, func(w http.ResponseWriter, r *http.Request) {
		chat(w, r, int(calls.Add(1)))
	})

	server := httptest.NewServer(mux)
	t.Setenv("WORKBUDDY_INTL_CHAT_BASE", server.URL)

	return server
}

func TestForwardRawSendsWorkbuddyIdentity(t *testing.T) {
	seedWorkbuddyAccounts(t, "wb-refresh-1")

	var gotHeader http.Header
	var gotBody map[string]any

	upstream := workbuddyUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		gotHeader = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer upstream.Close()

	proxy := NewProxy(workbuddyRegistry(upstream.URL, "wb-refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, route, _, err := proxy.ForwardRaw("/v1/chat/completions", workbuddyRequest(), map[string]any{
		"model":    "wb/deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if route.Dialect() != "workbuddy" {
		t.Fatalf("dialect = %q", route.Dialect())
	}
	if got := gotHeader.Get("Authorization"); got != "Bearer wb-access-2" {
		t.Fatalf("authorization = %q, want the refreshed access token", got)
	}
	for name, want := range map[string]string{
		"X-IDE-Type":     "WorkBuddy",
		"X-Product":      "SaaS",
		"X-Agent-Intent": "craft",
		"X-Private-Data": "true",
		"X-User-Id":      "uid-1",
	} {
		if gotHeader.Get(name) != want {
			t.Fatalf("%s = %q, want %q", name, gotHeader.Get(name), want)
		}
	}
	if gotHeader.Get("X-IDE-Version") != "5.8.4" {
		t.Fatalf("X-IDE-Version = %q, want the intl desktop version", gotHeader.Get("X-IDE-Version"))
	}
	if !strings.HasPrefix(gotHeader.Get("User-Agent"), "WorkBuddy/") {
		t.Fatalf("User-Agent = %q", gotHeader.Get("User-Agent"))
	}
	if gotHeader.Get("X-Conversation-Message-ID") == "" || gotHeader.Get("X-Conversation-ID") == "" {
		t.Fatalf("conversation id family missing: %#v", gotHeader)
	}
	if gotHeader.Get("X-Root-Request-ID") != gotHeader.Get("X-Conversation-Request-ID") {
		t.Fatalf("root request id must mirror the turn id")
	}
	if gotHeader.Get("X-B3-SpanId") == "" || gotHeader.Get("b3") == "" {
		t.Fatalf("trace family missing")
	}

	// Body: the default system message is prepended, stream is forced, usage
	// injection rides along.
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want system+user", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are a helpful assistant." {
		t.Fatalf("first message = %#v", first)
	}
	if gotBody["stream"] != true {
		t.Fatalf("stream = %v, want forced true", gotBody["stream"])
	}
}

func TestWorkbuddySanitizesDeveloperAndFingerprint(t *testing.T) {
	seedWorkbuddyAccounts(t, "wb-refresh-1")

	var gotBody map[string]any
	upstream := workbuddyUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer upstream.Close()

	proxy := NewProxy(workbuddyRegistry(upstream.URL, "wb-refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	_, _, _, err := proxy.ForwardRaw("/v1/chat/completions", workbuddyRequest(), map[string]any{
		"model": "wb/deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "developer", "content": "You are an AI agent powered by DeepSeek Harness."},
			map[string]any{"role": "user", "content": "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := gotBody["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("developer role survived: %#v", first)
	}
	if content, _ := first["content"].(string); strings.Contains(content, "powered by") {
		t.Fatalf("fingerprint sentence survived: %q", content)
	}
}

func TestWorkbuddyRefreshesOn401(t *testing.T) {
	seedWorkbuddyAccounts(t, "wb-refresh-1")

	var auth string
	upstream := workbuddyUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":401,"msg":"token expired"}`)
			return
		}
		auth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer upstream.Close()

	proxy := NewProxy(workbuddyRegistry(upstream.URL, "wb-refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", workbuddyRequest(), map[string]any{
		"model":    "wb/deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after refresh retry", resp.StatusCode)
	}
	if auth != "Bearer wb-access-2" {
		t.Fatalf("retry authorization = %q, want the refreshed token", auth)
	}
}

func TestWorkbuddyRefreshFailureMarksKeyDead(t *testing.T) {
	// No account in the store: the refresh stub answers 401 for the unknown
	// uid, and the key must come back dead — not retried forever.
	t.Setenv("WORKBUDDY_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "missing-wb-accounts.json"))

	var chatCalls atomic.Int64
	upstream := workbuddyUpstream(t, func(w http.ResponseWriter, r *http.Request, call int) {
		chatCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	defer upstream.Close()

	proxy := NewProxy(workbuddyRegistry(upstream.URL, "wb-refresh-unknown"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	_, _, _, err := proxy.ForwardRaw("/v1/chat/completions", workbuddyRequest(), map[string]any{
		"model":    "wb/deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err == nil {
		t.Fatal("expected exhaustion when the account refresh fails")
	}
	if got := chatCalls.Load(); got != 0 {
		t.Fatalf("chat calls = %d, want 0 (the request must die at refresh)", got)
	}
}
