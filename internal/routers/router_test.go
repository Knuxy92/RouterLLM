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
	router := New(handlers.New(nil), nil, nil)
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
	router := New(handlers.New(nil), log.New(&output, "", 0), nil)
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
