package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routerllm/internal/services"
)

// stubTest wires a Deps.Test closure that captures the parsed TestRequest so
// handler clamping and validation can be asserted without a live proxy.
func stubTest(t *testing.T, deps *Deps) *services.TestRequest {
	t.Helper()

	captured := &services.TestRequest{}
	deps.Test = func(r *http.Request, req services.TestRequest) *services.TestResult {
		*captured = req

		return &services.TestResult{Provider: req.Provider, UpstreamModel: req.Model, Status: 200}
	}

	return captured
}

func postTest(t *testing.T, srv http.Handler, session, body string) *httptest.ResponseRecorder {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPost, "/admin/api/providers/alpha/test", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	return w
}

func TestProviderTestClampsAndDefaults(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	captured := stubTest(t, &deps)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := postTest(t, srv, session, `{"model":"upstream-a","max_tokens":99999,"timeout_seconds":999,"effort":"high"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if captured.MaxTokens != testMaxTokensCap {
		t.Fatalf("max_tokens = %d, want clamped %d", captured.MaxTokens, testMaxTokensCap)
	}
	if captured.Timeout != testTimeoutCeiling*time.Second {
		t.Fatalf("timeout = %s, want clamped %d s", captured.Timeout, testTimeoutCeiling)
	}
	if captured.Prompt != testDefaultPromptTxt {
		t.Fatalf("prompt = %q, want default", captured.Prompt)
	}
	if captured.Provider != "alpha" {
		t.Fatalf("provider = %q, want from URL param", captured.Provider)
	}
	if captured.Effort != "high" {
		t.Fatalf("effort = %q, want high", captured.Effort)
	}
}

func TestProviderTestDefaultsToTwentySeconds(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	captured := stubTest(t, &deps)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := postTest(t, srv, session, `{"model":"upstream-a","prompt":"hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if captured.Timeout != 20*time.Second {
		t.Fatalf("timeout = %s, want default 20s", captured.Timeout)
	}
	if captured.MaxTokens != testDefaultTokens {
		t.Fatalf("max_tokens = %d, want default %d", captured.MaxTokens, testDefaultTokens)
	}
}

func TestProviderTestRejectsBadInput(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	stubTest(t, &deps)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing model", `{"prompt":"hi"}`, http.StatusBadRequest},
		{"bad effort", `{"model":"m","effort":"ultra"}`, http.StatusBadRequest},
		{"prompt too long", `{"model":"m","prompt":"` + strings.Repeat("x", testPromptCap+1) + `"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w := postTest(t, srv, session, tc.body); w.Code != tc.want {
			t.Fatalf("%s: status = %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestProviderTestNotWired(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	if w := postTest(t, srv, session, `{"model":"upstream-a"}`); w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", w.Code, w.Body.String())
	}
}
