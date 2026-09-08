package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"routerllm/internal/services"
)

const (
	testPromptCap        = 4000
	testMaxTokensCap     = 8192
	testDefaultTokens    = 256
	testDefaultTimeout   = 20
	testTimeoutFloor     = 5
	testTimeoutCeiling   = 120
	testDefaultPromptTxt = "Say 'pong' and nothing else."
)

// handleProviderTest runs a one-shot chat request pinned to a single provider.
// The raw fields are parsed and clamped here so bad input fails fast with a
// 400; the run itself (and its telemetry record) belongs to services.RunTest.
func (d Deps) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	if d.Test == nil {
		writeError(w, http.StatusNotImplemented, "provider test is not wired")
		return
	}

	var in struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		MaxTokens      int    `json:"max_tokens"`
		Effort         string `json:"effort"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if in.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(in.Prompt) > testPromptCap {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("prompt exceeds %d characters", testPromptCap))
		return
	}
	if in.Effort != "" && !validReasoningEffort[in.Effort] {
		writeError(w, http.StatusBadRequest, "reasoning effort must be one of: "+effortWhitelist())
		return
	}

	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = testDefaultTokens
	}
	maxTokens = min(maxTokens, testMaxTokensCap)

	timeout := in.TimeoutSeconds
	if timeout <= 0 {
		timeout = testDefaultTimeout
	}
	timeout = min(max(timeout, testTimeoutFloor), testTimeoutCeiling)

	if in.Prompt == "" {
		in.Prompt = testDefaultPromptTxt
	}

	result := d.Test(r, services.TestRequest{
		Provider:  chi.URLParam(r, "name"),
		Model:     in.Model,
		Prompt:    in.Prompt,
		MaxTokens: maxTokens,
		Effort:    in.Effort,
		Timeout:   time.Duration(timeout) * time.Second,
	})

	writeJSON(w, http.StatusOK, result)
}
