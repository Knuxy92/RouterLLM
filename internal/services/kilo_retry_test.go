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
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/settings"
)

// shrinkKiloRetry replaces the exponential base with 1µs for the test's
// lifetime — attempt 10 then sleeps at most ~512µs total.
func shrinkKiloRetry(t *testing.T) {
	t.Helper()
	prev := kiloRetryBase
	kiloRetryBase = time.Microsecond
	t.Cleanup(func() { kiloRetryBase = prev })
}

func writeKiloAccounts(t *testing.T, token string) {
	t.Helper()
	accounts := filepath.Join(t.TempDir(), "kilo-accounts.json")
	body := `{"accounts":[{"accountId":"acc_1","token":"` + token + `","organizationId":"org-1","createdAt":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(accounts, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_ACCOUNTS_FILE", accounts)
}

// The delay curve must match the Kilo CLI's session retry: 2s initial, ×2
// per attempt, up to 25% jitter, capped at 30s.
func TestKiloRetryDelayExponentialBounds(t *testing.T) {
	tests := []struct {
		attempt int
		lo, hi  time.Duration
	}{
		{1, 2 * time.Second, 2500 * time.Millisecond},
		{4, 16 * time.Second, 20 * time.Second},
		{5, kiloRetryCap, kiloRetryCap}, // 32s + jitter → capped
		{10, kiloRetryCap, kiloRetryCap},
	}
	for _, tt := range tests {
		for i := 0; i < 20; i++ { // jitter randomizes; sample repeatedly
			d := kiloRetryDelay(nil, tt.attempt)
			if d < tt.lo || d > tt.hi {
				t.Fatalf("attempt %d delay = %v, want [%v, %v]", tt.attempt, d, tt.lo, tt.hi)
			}
		}
	}
}

func TestKiloRetryDelayHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After-MS", "1500")
	if d := kiloRetryDelay(h, 1); d != 1500*time.Millisecond {
		t.Fatalf("Retry-After-MS delay = %v, want 1.5s", d)
	}

	h.Set("Retry-After-MS", "0") // the CLI accepts 0: retry immediately
	if d := kiloRetryDelay(h, 1); d != 0 {
		t.Fatalf("Retry-After-MS 0 delay = %v, want 0", d)
	}

	h.Del("Retry-After-MS")
	h.Set("Retry-After", "3")
	if d := kiloRetryDelay(h, 1); d != 3*time.Second {
		t.Fatalf("Retry-After seconds delay = %v, want 3s", d)
	}

	h.Set("Retry-After", "3600") // header value capped like the exponential path
	if d := kiloRetryDelay(h, 1); d != kiloRetryCap {
		t.Fatalf("Retry-After cap = %v, want %v", d, kiloRetryCap)
	}

	h.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	if d := kiloRetryDelay(h, 1); d <= 0 || d > 2*time.Second {
		t.Fatalf("Retry-After HTTP-date delay = %v, want (0, 2s]", d)
	}

	h.Set("Retry-After", "soon") // unusable → exponential fallback
	if d := kiloRetryDelay(h, 1); d < 2*time.Second || d > 2500*time.Millisecond {
		t.Fatalf("garbage Retry-After fell through = %v, want exponential 2s±jitter", d)
	}
}

// Kilo gets its own attempt count (10) regardless of the shared settings knob.
func TestForwardKiloRetriesTransientTenAttempts(t *testing.T) {
	shrinkKiloRetry(t)
	writeKiloAccounts(t, "tok_live")

	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 10 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"model_overloaded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(kiloRegistry(upstream.URL, "tok_live"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")), map[string]any{
		"model":    "kilo/auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after the retry window", resp.StatusCode)
	}
	if attempts != kiloRetryAttempts {
		t.Fatalf("upstream attempts = %d, want %d", attempts, kiloRetryAttempts)
	}
}

// Other styles keep the shared settings-backed retry: no kilo override leaks.
func TestForwardOpenAIRetryStaysAtSettingsAttempts(t *testing.T) {
	want := settings.Current().Retry.Attempts

	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name: "up", BaseURL: upstream.URL, Style: "openai", Keys: []string{"sk-1"}, AuthMode: "bearer",
	}}, []model.Rule{{
		ModelID: "up/m", Routes: []model.Spec{{Provider: "up", Model: "m"}},
	}}, time.Minute)
	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")

	resp, _, _, ferr := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")), map[string]any{
		"model":    "up/m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if ferr == nil {
		t.Fatal("forward unexpectedly succeeded on a persistent 503")
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if attempts != want {
		t.Fatalf("upstream attempts = %d, want the settings value %d", attempts, want)
	}
}

// A 429 carrying Retry-After-MS overrides the exponential wait: the default
// 2s base stays in place, so finishing well under it proves the header won.
func TestForwardKiloHonorsRetryAfterHeader(t *testing.T) {
	writeKiloAccounts(t, "tok_live")

	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After-MS", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(kiloRegistry(upstream.URL, "tok_live"), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")
	start := time.Now()
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")), map[string]any{
		"model":    "kilo/auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("upstream attempts = %d, want 2", attempts)
	}
	if elapsed := time.Since(start); elapsed >= kiloRetryBase {
		t.Fatalf("elapsed = %v, want < the 2s exponential base (header ignored?)", elapsed)
	}
}
