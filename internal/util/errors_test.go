package util

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeError(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()

	var got struct {
		Error errorBody `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error.Param != nil {
		t.Fatalf("param = %v, want null", got.Error.Param)
	}

	return got.Error
}

func TestWriteUpstreamErrorGeneric5xx(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 502)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	got := decodeError(t, w)
	if got.Code != "upstream_error" || got.Message != "service temporarily unavailable, please retry shortly" || got.Type != "server_error" {
		t.Fatalf("error = %+v", got)
	}
}

func TestWriteUpstreamErrorNormalizes5xxTo502(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 503)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	got := decodeError(t, w)
	if got.Code != "upstream_error" {
		t.Fatalf("error = %+v", got)
	}
}

func TestWriteUpstreamErrorGeneric4xx(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 400)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := decodeError(t, w)
	if got.Code != "invalid_request" || got.Message != "Bad Request" || got.Type != "invalid_request_error" {
		t.Fatalf("error = %+v", got)
	}
}

func TestWriteUpstreamErrorPreserves429(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 429)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	got := decodeError(t, w)
	if got.Code != "rate_limited" || got.Message != "rate limit exceeded, please retry shortly" {
		t.Fatalf("error = %+v", got)
	}
}

func TestWriteOverloadedError(t *testing.T) {
	w := httptest.NewRecorder()

	WriteOverloadedError(w)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	got := decodeError(t, w)
	if got.Code != "model_overloaded" || got.Message != "model is currently overloaded, please retry shortly" || got.Type != "server_error" {
		t.Fatalf("error = %+v", got)
	}
}
