package util

import (
	"encoding/json"
	"net/http"
	"strings"
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

func WriteUpstreamError(w http.ResponseWriter, status int, body []byte) {
	var response errorResponse
	if err := json.Unmarshal(body, &response); err == nil && response.Error.Message != "" {
		if response.Error.Code == "" {
			response.Error.Code = "upstream_error"
		}
		if response.Error.Type == "" {
			response.Error.Type = errorType(status)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	var anthropicResponse struct {
		Error errorBody `json:"error"`
	}
	if err := json.Unmarshal(body, &anthropicResponse); err == nil && anthropicResponse.Error.Message != "" {
		code := anthropicResponse.Error.Code
		if code == "" {
			code = "upstream_error"
		}
		WriteError(w, status, code, anthropicResponse.Error.Message)
		return
	}

	message := strings.TrimSpace(string(body))
	WriteError(w, status, "upstream_error", message)
}

func errorType(status int) string {
	if status >= 400 && status < 500 {
		return "invalid_request_error"
	}
	return "server_error"
}
