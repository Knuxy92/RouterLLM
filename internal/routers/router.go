package routers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"routerllm/internal/handlers"
	"routerllm/internal/util"
)

const auditBodyLimit = 10 << 20

func New(h *handlers.Handlers, logger *log.Logger, authToken func() string, mountAdmin func(chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID, auditLog(logger), chimw.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(requireBearer(authToken))

		r.Get("/models", h.Models)
		r.Post("/chat/completions", h.ChatCompletions)
		r.Post("/messages", h.Messages)
		r.Post("/responses", h.Responses)
		r.HandleFunc("/files", h.Files)
		r.HandleFunc("/files/*", h.Files)
	})

	if mountAdmin != nil {
		mountAdmin(r)
	}

	return r
}

func requireBearer(authToken func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := ""
			if authToken != nil {
				raw = authToken()
			}
			tokens := validTokens(raw)
			if len(tokens) == 0 || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			supplied := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !matchesAny(supplied, tokens) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="routerllm"`)
				util.WriteError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid bearer token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func validTokens(raw string) []string {
	parts := strings.Split(raw, ",")
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			tokens = append(tokens, part)
		}
	}

	return tokens
}

func matchesAny(supplied string, tokens []string) bool {
	if supplied == "" {
		return false
	}

	sum := sha256.Sum256([]byte(supplied))
	match := 0
	for _, token := range tokens {
		candidate := sha256.Sum256([]byte(token))
		match |= subtle.ConstantTimeCompare(sum[:], candidate[:])
	}

	return match == 1
}

type auditResponseWriter struct {
	http.ResponseWriter
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	remaining := auditBodyLimit - w.body.Len()
	if remaining > 0 {
		captured := body
		if len(captured) > remaining {
			captured = captured[:remaining]
		}
		_, _ = w.body.Write(captured)
	}
	if len(body) > remaining {
		w.truncated = true
	}

	return w.ResponseWriter.Write(body)
}

func (w *auditResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func auditLog(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if logger == nil {
				next.ServeHTTP(w, r)
				return
			}

			started := time.Now()
			requestBody, readErr := captureRequestBody(r)
			logger.Printf("user_request time=%s request_id=%s method=%s path=%s headers=%s body=%q body_truncated=%t read_error=%v",
				started.Format(time.RFC3339Nano), r.Header.Get("X-Request-ID"), r.Method, r.URL.RequestURI(), formatHeaders(r.Header), requestBody.body, requestBody.truncated, readErr)

			writer := &auditResponseWriter{ResponseWriter: w}
			next.ServeHTTP(writer, r)
			if writer.status == 0 {
				writer.status = http.StatusOK
			}

			logger.Printf("system_response time=%s request_id=%s status=%d duration=%s headers=%s body=%q body_truncated=%t",
				time.Now().Format(time.RFC3339Nano), r.Header.Get("X-Request-ID"), writer.status, time.Since(started), formatHeaders(writer.Header()), writer.body.String(), writer.truncated)
		})
	}
}

type capturedBody struct {
	body      string
	truncated bool
}

func captureRequestBody(r *http.Request) (capturedBody, error) {
	if r.Body == nil {
		return capturedBody{}, nil
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, auditBodyLimit+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), r.Body))
	if len(raw) > auditBodyLimit {
		return capturedBody{body: string(raw[:auditBodyLimit]), truncated: true}, err
	}

	return capturedBody{body: string(raw)}, err
}

func formatHeaders(headers http.Header) string {
	clean := headers.Clone()
	for name := range clean {
		lowerName := strings.ToLower(name)
		if lowerName == "authorization" || lowerName == "x-api-key" || strings.Contains(lowerName, "token") {
			clean.Set(name, "[REDACTED]")
		}
	}

	encoded, _ := json.Marshal(clean)
	return string(encoded)
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				http.Error(w, "failed to create request id", http.StatusInternalServerError)
				return
			}
			id = hex.EncodeToString(raw[:])
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}
