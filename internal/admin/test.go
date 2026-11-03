package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"routerllm/internal/services"
	"routerllm/internal/settings"
	"routerllm/internal/util"
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
		StyleCall      string `json:"stylecall"`
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
	limits := settings.Current().Admin.Test
	if len(in.Prompt) > limits.PromptBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("prompt exceeds %d characters", limits.PromptBytes))
		return
	}
	if in.Effort != "" && !validReasoningEffort[in.Effort] {
		writeError(w, http.StatusBadRequest, "reasoning effort must be one of: "+effortWhitelist())
		return
	}
	if in.StyleCall != "" && !validStyleCall[in.StyleCall] {
		writeError(w, http.StatusBadRequest, "stylecall must be one of: "+styleCallWhitelist())
		return
	}

	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = limits.DefaultTokens
	}
	maxTokens = min(maxTokens, limits.MaxTokens)

	timeout := in.TimeoutSeconds
	if timeout <= 0 {
		timeout = limits.DefaultTimeoutSeconds
	}
	timeout = min(max(timeout, limits.TimeoutFloorSeconds), limits.TimeoutCeilingSeconds)

	if in.Prompt == "" {
		in.Prompt = limits.DefaultPrompt
	}

	result := d.Test(r, services.TestRequest{
		Provider:  chi.URLParam(r, "name"),
		Model:     in.Model,
		Prompt:    in.Prompt,
		MaxTokens: maxTokens,
		Effort:    in.Effort,
		StyleCall: in.StyleCall,
		Timeout:   time.Duration(timeout) * time.Second,
	})

	util.WriteJSON(w, http.StatusOK, result)
}
