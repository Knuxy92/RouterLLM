package routers

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routerllm/internal/handlers"
)

func TestRemovedRoutesReturnNotFound(t *testing.T) {
	router := New(handlers.New(nil), nil, nil, nil)
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodPost, "/v1/images/generations"},
		{http.MethodPost, "/v1/images/edits"},
		{http.MethodPost, "/v1/images/variations"},
		{http.MethodPost, "/v1/audio/speech"},
		{http.MethodPost, "/v1/audio/transcriptions"},
		{http.MethodPost, "/v1/moderations"},
		{http.MethodPost, "/v1/rerank"},
		{http.MethodPost, "/v1/batches"},
		{http.MethodGet, "/v1/batches"},
		{http.MethodGet, "/v1/batches/id"},
		{http.MethodPost, "/v1/batches/id/cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
		})
	}
}

func TestAuditLogUsesConfiguredLogger(t *testing.T) {
	var output bytes.Buffer
	router := New(handlers.New(nil), log.New(&output, "", 0), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/missing", strings.NewReader(`{"message":"hello"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Test", "value")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	logs := output.String()
	for _, want := range []string{
		"user_request time=",
		"system_response time=",
		`body="{\"message\":\"hello\"}"`,
		`"Authorization":["[REDACTED]"]`,
		`"X-Test":["value"]`,
		"status=404",
		"duration=",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "Bearer secret") {
		t.Fatalf("authorization leaked into log:\n%s", logs)
	}
}

// handlers.New(nil) has no proxy, so assertions never reach a real handler:
// 401 always means the gate stopped the request; 404/405 on an unknown route
// or method means the gate let it through to chi's routing.
func TestV1BearerGate(t *testing.T) {
	t.Setenv("AUTHTOKEN", "")
	open := New(handlers.New(nil), nil, func() string { return "" }, nil)

	// Gate disabled: a POST reaches chi routing (404), never 401.
	if w := serve(open, http.MethodPost, "/v1/embeddings", ""); w.Code != http.StatusNotFound {
		t.Fatalf("POST with no AUTHTOKEN configured = %d, want 404 (gate disabled)", w.Code)
	}
	if w := serve(open, http.MethodGet, "/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health = %d, want 200", w.Code)
	}

	gated := New(handlers.New(nil), nil, func() string { return "primary, secondary " }, nil)

	// Missing and wrong tokens are rejected with a challenge header.
	w := serve(gated, http.MethodPost, "/v1/embeddings", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST without token = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("missing WWW-Authenticate on 401")
	}
	if w := serve(gated, http.MethodPost, "/v1/embeddings", "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("POST with wrong token = %d, want 401", w.Code)
	}

	// The second token in the comma list works, and 404 proves the request
	// passed the gate and reached chi routing.
	if w := serve(gated, http.MethodPost, "/v1/embeddings", "secondary"); w.Code != http.StatusNotFound {
		t.Fatalf("POST with valid token = %d, want 404 (gate passed)", w.Code)
	}

	// Reads stay open even with the gate on.
	if w := serve(gated, http.MethodGet, "/v1/embeddings", ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET unknown route with gate on = %d, want 404 (gate skipped)", w.Code)
	}
	if w := serve(gated, http.MethodGet, "/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health behind gate = %d, want 200", w.Code)
	}
}

func TestValidTokensSplitsAndTrims(t *testing.T) {
	if got := validTokens(""); len(got) != 0 {
		t.Fatalf("validTokens(\"\") = %v, want empty", got)
	}
	if got := validTokens(" , a ,,b, "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("validTokens = %v, want [a b]", got)
	}
	if !matchesAny("secret", []string{"nope", "secret"}) {
		t.Fatal("matchesAny rejected a listed token")
	}
	if matchesAny("", []string{"secret"}) {
		t.Fatal("matchesAny accepted an empty header")
	}
}

func serve(r http.Handler, method, target, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}
