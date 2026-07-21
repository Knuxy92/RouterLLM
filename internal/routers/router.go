package routers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"routerllm/internal/handlers"
)

func New(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Logger, chimw.Recoverer, cors)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/v1", func(r chi.Router) {
		r.Get("/models", h.Models)
		r.Post("/chat/completions", h.ChatCompletions)
		r.Post("/messages", h.Messages)
		r.Post("/responses", h.Responses)
		r.Post("/embeddings", h.Embeddings)
		r.Post("/images/generations", h.ImagesGenerations)
		r.Post("/images/edits", h.ImagesEdits)
		r.Post("/images/variations", h.ImagesVariations)
		r.Post("/audio/speech", h.AudioSpeech)
		r.Post("/audio/transcriptions", h.AudioTranscriptions)
		r.Post("/moderations", h.Moderations)
		r.Post("/rerank", h.Rerank)
		r.Post("/batches", h.BatchesCreate)
		r.Get("/batches", h.BatchesList)
		r.Get("/batches/{id}", h.BatchesRetrieve)
		r.Post("/batches/{id}/cancel", h.BatchesCancel)
	})

	return r
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
