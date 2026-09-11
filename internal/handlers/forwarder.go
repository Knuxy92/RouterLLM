package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"routerllm/internal/adapter"
	"routerllm/internal/model"
	"routerllm/internal/services"
	"routerllm/internal/telemetry"
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

	ctx, trace := services.WithRequestTrace(r.Context())
	r = r.WithContext(ctx)

	modelName := extractModel(raw)
	reqID := r.Header.Get("X-Request-Id")

	resp, route, err := h.proxy.ForwardRaw("/v1/chat/completions", r, reqBody)
	if resp != nil {
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "text/event-stream") {
			eb := services.ReadErrorBody(resp.Body)
			util.WriteUpstreamError(w, resp.StatusCode, services.TruncateErrorBody(eb))

			note := ""
			if err != nil {
				note = err.Error()
			}
			h.proxy.RecordTelemetry(trace.Event(modelName, reqID, resp.StatusCode, note, respTokens(resp), telemetry.ClampBody(eb, telemetry.RespBodyCap)))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		switch route.Dialect() {
		case "messages":
			_ = util.StreamRawSSE(resp.Body, w)
		case "google":
			adapter.StreamGoogleToAnthropicSSE(resp.Body, w, modelName)
		case "responses":
			adapter.StreamResponsesToAnthropicSSE(resp.Body, w, modelName)
		default:
			adapter.StreamOpenAIToAnthropicSSE(resp.Body, w, modelName)
		}

		h.proxy.RecordTelemetry(trace.Event(modelName, reqID, http.StatusOK, "", respTokens(resp), ""))
		return
	}

	if err != nil {
		util.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		h.proxy.RecordTelemetry(trace.Event(modelName, reqID, http.StatusBadGateway, err.Error(), 0, ""))
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

// respTokens reads the completion token count captured by the telemetry
// watcher ForwardRaw attaches to the upstream body, 0 when none is present.
func respTokens(resp *http.Response) int {
	if w, ok := resp.Body.(*telemetry.Watcher); ok {
		return w.Tokens()
	}

	return 0
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
