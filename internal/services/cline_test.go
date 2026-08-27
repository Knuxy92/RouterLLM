package services

import (
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
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func clineRegistry(baseURL string, refreshTokens ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "cline", BaseURL: baseURL, Style: "cline", Keys: refreshTokens,
	}}, []model.Rule{{
		ModelID: "cline-free/glm-5.2", Routes: []model.Spec{{Provider: "cline", Model: "cline-free/glm-5.2"}},
	}}, time.Minute)
}

func clineRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
}

func TestForwardRawSendsClineHeadersAndSession(t *testing.T) {
	t.Setenv("CLINE_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "cline-accounts.json"))

	var gotHeader http.Header
	var gotBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access-1",
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := NewProxy(clineRegistry(upstream.URL, "refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, route, err := proxy.ForwardRaw("/v1/chat/completions", clineRequest(), map[string]any{
		"model":    "cline-free/glm-5.2",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if route.Provider.Style != "cline" {
		t.Fatalf("style = %q", route.Provider.Style)
	}
	if got := gotHeader.Get("Authorization"); got != "Bearer workos:access-1" {
		t.Fatalf("authorization = %q", got)
	}
	sessionID, _ := gotBody["session_id"].(string)
	if sessionID == "" || gotHeader.Get("X-Task-ID") != sessionID {
		t.Fatalf("session id = %q, task id = %q", sessionID, gotHeader.Get("X-Task-ID"))
	}
	for key, want := range map[string]string{
		"X-CLIENT-TYPE":    "cline-sdk",
		"X-PLATFORM":       "terminal",
		"X-CLIENT-VERSION": "3.0.47",
	} {
		if got := gotHeader.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if gotBody["max_tokens"] == nil || gotBody["reasoning_effort"] != "high" {
		t.Fatalf("body defaults = %#v", gotBody)
	}
}

func TestForwardRawRefreshesClineTokenOnUnauthorized(t *testing.T) {
	t.Setenv("CLINE_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "cline-accounts.json"))

	refreshes := 0
	attempts := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access-" + string(rune('0'+refreshes)),
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"expired"}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer workos:access-2" {
			t.Errorf("retry authorization = %q, want refreshed token", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := NewProxy(clineRegistry(upstream.URL, "refresh-1"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, _, err := proxy.ForwardRaw("/v1/chat/completions", clineRequest(), map[string]any{
		"model":    "cline-free/glm-5.2",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 || refreshes != 2 {
		t.Fatalf("attempts = %d, refreshes = %d, want 2 and 2", attempts, refreshes)
	}
}

func TestForwardRawRotatesClineAccountsAfterUnauthorized(t *testing.T) {
	t.Setenv("CLINE_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "cline-accounts.json"))

	seen := map[string]int{}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access-for-" + payload["refreshToken"],
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer workos:access-for-")
		seen[token]++
		if token == "refresh-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"expired"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := NewProxy(clineRegistry(upstream.URL, "refresh-1", "refresh-2"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, _, err := proxy.ForwardRaw("/v1/chat/completions", clineRequest(), map[string]any{
		"model":    "cline-free/glm-5.2",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if seen["refresh-2"] == 0 {
		t.Fatalf("expected failover to second account, saw %#v", seen)
	}
}
