package util

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteUpstreamErrorNormalizesPlainText(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 502, []byte("provider unavailable"))

	var got struct {
		Error errorBody `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error.Code != "upstream_error" || got.Error.Message != "provider unavailable" || got.Error.Type != "server_error" {
		t.Fatalf("error = %+v", got.Error)
	}
	if got.Error.Param != nil {
		t.Fatalf("param = %v, want null", got.Error.Param)
	}
}

func TestWriteUpstreamErrorPreservesOpenAIShape(t *testing.T) {
	w := httptest.NewRecorder()

	WriteUpstreamError(w, 400, []byte(`{"error":{"code":"bad_model","message":"unknown model","type":"invalid_request_error"}}`))

	var got struct {
		Error errorBody `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error.Code != "bad_model" || got.Error.Message != "unknown model" || got.Error.Type != "invalid_request_error" {
		t.Fatalf("error = %+v", got.Error)
	}
}
