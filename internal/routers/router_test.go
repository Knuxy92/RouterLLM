package routers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"routerllm/internal/handlers"
)

func TestRemovedRoutesReturnNotFound(t *testing.T) {
	router := New(handlers.New(nil, nil))
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
