package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"routerllm/internal/adapter"
	"routerllm/internal/model"
	"routerllm/internal/services"
	"routerllm/internal/util"
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

func (h *Handlers) Messages(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	openaiBody, err := adapter.AnthropicRequestToOpenAI(raw)
	if err != nil {
		http.Error(w, "invalid Anthropic request: "+err.Error(), http.StatusBadRequest)
		return
	}

	cw := &captureWriter{header: make(http.Header)}
	r2 := *r
	r2.Body = io.NopCloser(bytes.NewReader(openaiBody))
	h.proxy.Forward("/v1/chat/completions", cw, &r2)

	body := cw.buf.Bytes()
	ct := cw.header.Get("Content-Type")
	if cw.code != http.StatusOK || body == nil {
		for k, v := range cw.header {
			w.Header()[k] = v
		}
		w.WriteHeader(cw.code)
		w.Write(body)
		return
	}

	if ct == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		modelVal := extractModel(raw)
		_, _ = util.IterDataLines(bytes.NewReader(body), func(payload string) bool {
			if payload == "[DONE]" {
				fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return false
			}
			translated := adapter.OpenAIStreamToAnthropicSSE([]byte(payload), modelVal)
			w.Write(translated)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return true
		})
		return
	}

	anthropicBody, err := adapter.OpenAIResponseToAnthropic(body)
	if err != nil {
		http.Error(w, "failed to translate response: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(anthropicBody)
}

func extractModel(raw []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &v)
	return v.Model
}

type captureWriter struct {
	header http.Header
	buf    bytes.Buffer
	code   int
}

func (w *captureWriter) Header() http.Header { return w.header }
func (w *captureWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *captureWriter) WriteHeader(code int) { w.code = code }

func (h *Handlers) Embeddings(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/embeddings", w, r)
}

func (h *Handlers) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/images/generations", w, r)
}

func (h *Handlers) ImagesEdits(w http.ResponseWriter, r *http.Request) {
	h.proxy.PassthroughMultipart("/v1/images/edits", w, r)
}

func (h *Handlers) ImagesVariations(w http.ResponseWriter, r *http.Request) {
	h.proxy.PassthroughMultipart("/v1/images/variations", w, r)
}

func (h *Handlers) AudioSpeech(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/audio/speech", w, r)
}

func (h *Handlers) AudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	h.proxy.PassthroughMultipart("/v1/audio/transcriptions", w, r)
}

func (h *Handlers) Moderations(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/moderations", w, r)
}

func (h *Handlers) Rerank(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/rerank", w, r)
}

func (h *Handlers) BatchesCreate(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough("/v1/batches", w, r)
}

func (h *Handlers) BatchesList(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough(r.URL.Path, w, r)
}

func (h *Handlers) BatchesRetrieve(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough(r.URL.Path, w, r)
}

func (h *Handlers) BatchesCancel(w http.ResponseWriter, r *http.Request) {
	h.proxy.Passthrough(r.URL.Path, w, r)
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
