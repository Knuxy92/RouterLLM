package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"routerllm/internal/adapter"
	"routerllm/internal/model"
	"routerllm/internal/services"
	"routerllm/internal/util"
)

type Handlers struct {
	proxy *services.Proxy
}

func New(proxy *services.Proxy) *Handlers {
	return &Handlers{proxy: proxy}
}

func (h *Handlers) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.proxy.Forward("/v1/chat/completions", w, r)
}

func (h *Handlers) Responses(w http.ResponseWriter, r *http.Request) {
	h.proxy.Forward("/v1/responses", w, r)
}

func (h *Handlers) Files(w http.ResponseWriter, r *http.Request) {
	h.proxy.ForwardFile(w, r)
}

func (h *Handlers) Messages(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to read body: "+err.Error())
		return
	}

	openaiBody, err := adapter.AnthropicRequestToOpenAI(raw)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid Anthropic request: "+err.Error())
		return
	}

	var reqBody map[string]any
	if err := json.Unmarshal(openaiBody, &reqBody); err != nil {
		util.WriteError(w, http.StatusInternalServerError, "translation_error", "internal translation error: "+err.Error())
		return
	}
	reqBody["stream"] = true

	resp, route, err := h.proxy.ForwardRaw("/v1/chat/completions", r, reqBody)
	if resp != nil {
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "text/event-stream") {
			eb, _ := io.ReadAll(resp.Body)
			util.WriteUpstreamError(w, resp.StatusCode, eb)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if route.Provider.Style == "anthropic" {
			_ = util.StreamRawSSE(resp.Body, w)
		} else {
			adapter.StreamOpenAIToAnthropicSSE(resp.Body, w, extractModel(raw))
		}
		return
	}

	if err != nil {
		util.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
}

func extractModel(raw []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &v)
	return v.Model
}

func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	models := h.proxy.Models()
	data := make([]model.Model, 0, len(models))

	for _, id := range models {
		data = append(data, model.Model{
			ID:      id,
			Object:  "model",
			Created: 1710000000,
			OwnedBy: "RouterLLM",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.ModelList{Object: "list", Data: data})
}
