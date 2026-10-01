package util

import (
	"encoding/json"
	"net/http"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   any    `json:"param"`
	Type    string `json:"type"`
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	if code == "" {
		code = "internal_error"
	}
	if message == "" {
		message = http.StatusText(status)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: errorBody{
		Code:    code,
		Message: message,
		Type:    errorType(status),
	}})
}

func WriteOverloadedError(w http.ResponseWriter) {
	WriteError(w, http.StatusServiceUnavailable, "model_overloaded", "model is currently overloaded, please retry shortly")
}

// WriteUpstreamError maps an upstream failure to a generic client-facing
// error. The upstream body is intentionally not reflected: it may carry
// provider names, model mappings, or quota internals. Raw detail stays in
// server logs and admin telemetry.
func WriteUpstreamError(w http.ResponseWriter, status int) {
	if status == http.StatusTooManyRequests {
		WriteError(w, status, "rate_limited", "rate limit exceeded, please retry shortly")
		return
	}

	if status >= 400 && status < 500 {
		WriteError(w, status, "invalid_request", http.StatusText(status))
		return
	}

	WriteError(w, http.StatusBadGateway, "upstream_error", "service temporarily unavailable, please retry shortly")
}

func errorType(status int) string {
	if status >= 400 && status < 500 {
		return "invalid_request_error"
	}
	return "server_error"
}
