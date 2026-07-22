package routers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"routerllm/internal/handlers"
)

func New(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Logger, chimw.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/v1", func(r chi.Router) {
		r.Get("/models", h.Models)
		r.Post("/chat/completions", h.ChatCompletions)
		r.Post("/messages", h.Messages)
		r.Post("/responses", h.Responses)
	})

	return r
}
