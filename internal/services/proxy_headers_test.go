package services

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// headerCaptureUpstream starts a fake openai-style upstream that records the
// headers of every request it receives.
func headerCaptureUpstream(t *testing.T) (*httptest.Server, func() http.Header) {
	t.Helper()

	var mu sync.Mutex
	var captured http.Header

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.Header.Clone()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"test"}`)
	}))
	t.Cleanup(upstream.Close)

	return upstream, func() http.Header {
		mu.Lock()
		defer mu.Unlock()

		return captured
	}
}

func newHeaderTestRegistry(upstream *httptest.Server) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"provider-key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)
}

// TestForwardRawClientCredentialsDenied covers the unconditional deny-list: a
// client's own credentials, cookies and encoding preferences never reach an
// upstream, even with forwarding on and even when the allowlist names them.
func TestForwardRawClientCredentialsDenied(t *testing.T) {
	tests := []struct {
		name  string
		allow []string
	}{
		{name: "default forward-all"},
		{name: "allowlist names credentials", allow: []string{"authorization", "cookie", "x-api-key", "x-goog-api-key", "accept-encoding", "x-client-header"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream, headers := headerCaptureUpstream(t)
			proxy := NewProxy(newHeaderTestRegistry(upstream), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, tt.allow, "")

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Authorization", "Bearer client-token")
			req.Header.Set("X-Api-Key", "client-key")
			req.Header.Set("X-Goog-Api-Key", "client-gkey")
			req.Header.Set("Cookie", "session=client-cookie")
			req.Header.Set("Cookie2", "session2=client-cookie2")
			req.Header.Set("Proxy-Authorization", "Basic client-secret")
			req.Header.Set("Accept-Encoding", "identity")
			req.Header.Set("X-Client-Header", "client-value")

			resp, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
				"model":    "test-model",
				"messages": []any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			got := headers()
			if v := got.Get("Authorization"); v != "Bearer provider-key" {
				t.Errorf("Authorization = %q, want provider key", v)
			}
			for _, h := range []string{"X-Api-Key", "X-Goog-Api-Key", "Cookie", "Cookie2", "Proxy-Authorization"} {
				if v := got.Get(h); v != "" {
					t.Errorf("%s = %q, must not reach the upstream", h, v)
				}
			}
			if v := got.Get("Accept-Encoding"); v == "identity" {
				t.Errorf("Accept-Encoding = %q, must not be copied from the client", v)
			}
			if v := got.Get("X-Client-Header"); v != "client-value" {
				t.Errorf("X-Client-Header = %q, want forwarded — the deny-list must not block it", v)
			}
		})
	}
}

// TestApplySettingsHotReload checks that the request-shaping switches take
// effect on a live Proxy after ApplySettings, without a restart.
func TestApplySettingsHotReload(t *testing.T) {
	upstream, headers := headerCaptureUpstream(t)
	proxy := NewProxy(newHeaderTestRegistry(upstream), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, true, nil, "")

	send := func() http.Header {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Client-Header", "client-value")
		req.Header.Set("X-Other-Header", "other-value")
		resp, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
			"model":    "test-model",
			"messages": []any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		return headers()
	}

	if got := send(); got.Get("X-Client-Header") != "client-value" {
		t.Fatalf("before ApplySettings: X-Client-Header = %q, want forwarded", got.Get("X-Client-Header"))
	}

	proxy.ApplySettings(false, false, nil)
	if got := send(); got.Get("X-Client-Header") != "" || got.Get("X-Other-Header") != "" {
		t.Fatalf("after ApplySettings(forward=false): headers = %v, want none forwarded", got)
	}

	proxy.ApplySettings(false, true, []string{"x-other-header"})
	got := send()
	if got.Get("X-Client-Header") != "" {
		t.Fatalf("after allowlist swap: X-Client-Header = %q, want dropped", got.Get("X-Client-Header"))
	}
	if got.Get("X-Other-Header") != "other-value" {
		t.Fatalf("after allowlist swap: X-Other-Header = %q, want forwarded", got.Get("X-Other-Header"))
	}

	t.Run("force stream applies on reload", func(t *testing.T) {
		gUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, googleUpstreamSSE)
		}))
		defer gUpstream.Close()

		gProxy := NewProxy(newGoogleRegistry(gUpstream, []string{"gkey"}), gUpstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
		gProxy.ApplySettings(true, false, nil)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
			`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
		w := httptest.NewRecorder()

		gProxy.Forward("/v1/chat/completions", w, req)

		if !strings.Contains(w.Body.String(), "event: message_start") {
			t.Fatalf("force stream not applied after ApplySettings:\n%s", w.Body.String())
		}
	})
}
