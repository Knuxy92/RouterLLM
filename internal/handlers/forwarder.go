package handlers

import (
	"net/http"

	"agentrouter/internal/model"
	"agentrouter/internal/services"

	"github.com/gin-gonic/gin"
)

type Handlers struct {
	proxy   *services.Proxy
	models  []string
}

func New(proxy *services.Proxy, models []string) *Handlers {
	return &Handlers{proxy: proxy, models: models}
}

func (h *Handlers) ChatCompletions(c *gin.Context) {
	h.proxy.Forward("/v1/chat/completions", c)
}

func (h *Handlers) Responses(c *gin.Context) {
	h.proxy.Forward("/v1/responses", c)
}

func (h *Handlers) Models(c *gin.Context) {
	data := make([]model.Model, 0, len(h.models))
	for _, id := range h.models {
		data = append(data, model.Model{
			ID:      id,
			Object:  "model",
			Created: 1710000000,
			OwnedBy: "agentrouter",
		})
	}
	c.JSON(http.StatusOK, model.ModelList{Object: "list", Data: data})
}
