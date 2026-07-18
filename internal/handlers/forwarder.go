package handlers

import (
	"encoding/json"
	"net/http"

	"routerllm/internal/model"
	"routerllm/internal/services"
)

type Handlers struct {
	proxy  *services.Proxy
	models []string
}

func New(proxy *services.Proxy, models []string) *Handlers {
	return &Handlers{proxy: proxy, models: models}
}

func (h *Handlers) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.proxy.Forward("/v1/chat/completions", w, r)
}

func (h *Handlers) Responses(w http.ResponseWriter, r *http.Request) {
	h.proxy.Forward("/v1/responses", w, r)
}

func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	data := make([]model.Model, 0, len(h.models))
	for _, id := range h.models {
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
