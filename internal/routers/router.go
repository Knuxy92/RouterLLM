package routers

import (
	"log"
	"net/http"
	"time"

	"routerllm/internal/handlers"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func New(h *handlers.Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /v1/models", h.Models)
	mux.HandleFunc("POST /v1/chat/completions", h.ChatCompletions)
	mux.HandleFunc("POST /v1/messages", h.Messages)
	mux.HandleFunc("POST /v1/responses", h.Responses)
	mux.HandleFunc("POST /v1/embeddings", h.Embeddings)
	mux.HandleFunc("POST /v1/images/generations", h.ImagesGenerations)
	mux.HandleFunc("POST /v1/images/edits", h.ImagesEdits)
	mux.HandleFunc("POST /v1/images/variations", h.ImagesVariations)
	mux.HandleFunc("POST /v1/audio/speech", h.AudioSpeech)
	mux.HandleFunc("POST /v1/audio/transcriptions", h.AudioTranscriptions)
	mux.HandleFunc("POST /v1/moderations", h.Moderations)
	mux.HandleFunc("POST /v1/rerank", h.Rerank)
	mux.HandleFunc("POST /v1/batches", h.BatchesCreate)
	mux.HandleFunc("GET /v1/batches", h.BatchesList)
	mux.HandleFunc("GET /v1/batches/{id}", h.BatchesRetrieve)
	mux.HandleFunc("POST /v1/batches/{id}/cancel", h.BatchesCancel)

	return chain(mux, recovery, logger, cors)
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lw.status, time.Since(start))
	})
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
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
