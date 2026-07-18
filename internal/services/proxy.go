package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"routerllm/internal/adapter"
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/util"
)

const maxRetries = 3

var deadStatuses = map[int]bool{
	401: true, 402: true, 403: true,
}

var transientStatuses = map[int]bool{
	408: true, 429: true, 500: true, 502: true, 503: true, 504: true,
}

type Proxy struct {
	registry *provider.Registry
	client   *http.Client
	log      *log.Logger
}

func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger) *Proxy {
	return &Proxy{registry: reg, client: client, log: log}
}

func (p *Proxy) Forward(path string, w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	body, clientStream, err := parseAndForceStream(raw)
	if err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if path == "/v1/responses" {
		body["stream"] = clientStream
	}

	model, _ := body["model"].(string)
	routes := p.registry.Routes(model)
	if len(routes) == 0 {
		http.Error(w, fmt.Sprintf("model %q not found", model), http.StatusNotFound)
		return
	}

	if path != "/v1/chat/completions" {
		var filtered []provider.Route
		for _, r := range routes {
			if r.Provider.Style == "openai" {
				filtered = append(filtered, r)
			}
		}
		routes = filtered
		if len(routes) == 0 {
			http.Error(w, fmt.Sprintf("model %q not available for %s", model, path), http.StatusNotFound)
			return
		}
	}

	p.log.Printf("%s model=%s stream=%v routes=%d", path, model, clientStream, len(routes))

	var lastStatus int
	var lastErrBody []byte

	for _, route := range routes {
		pv := route.Provider
		routeBody := cloneBody(body)
		applyDefaults(routeBody, route.Defaults)

		var reqBody []byte
		var reqPath string

		if pv.Style == "anthropic" {
			reqBody, reqPath, err = adapter.TranslateRequest(routeBody, route.ModelName)
		} else {
			routeBody["model"] = route.ModelName
			reqBody, err = json.Marshal(routeBody)
			reqPath = path
		}
		if err != nil {
			http.Error(w, "failed to encode body: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp, status, errBody, served := p.tryKeys(pv, reqPath, reqBody, r)
		if served {
			return
		}
		if resp == nil {
			p.log.Printf("all keys exhausted for %s via %s, falling back...", model, pv.Name)
			lastStatus = status
			lastErrBody = errBody
			continue
		}

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			p.log.Printf("upstream %s returned HTTP %d, passing through", pv.Name, resp.StatusCode)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(eb)
			return
		}

		p.log.Printf("serving %s via %s (HTTP %d)", path, pv.Name, resp.StatusCode)

		if pv.Style == "anthropic" {
			p.serveAnthropic(resp, clientStream, route.ModelName, w)
		} else if path == "/v1/responses" {
			serveResponses(resp, clientStream, w)
		} else {
			serveOpenAI(resp, clientStream, w)
		}
		return
	}

	if lastErrBody != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastStatus)
		w.Write(lastErrBody)
	} else {
		http.Error(w, "all providers exhausted for model "+model, http.StatusBadGateway)
	}
}

func parseAndForceStream(raw []byte) (map[string]any, bool, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	clientStream, _ := body["stream"].(bool)
	body["stream"] = true
	return body, clientStream, nil
}

func cloneBody(body map[string]any) map[string]any {
	clone := make(map[string]any, len(body))
	for k, v := range body {
		clone[k] = v
	}
	return clone
}

func applyDefaults(body map[string]any, defaults model.RequestDefaults) {
	if defaults.ReasoningEffort != "" {
		if _, ok := body["reasoning_effort"]; !ok {
			body["reasoning_effort"] = defaults.ReasoningEffort
		}
	}
	if defaults.EnableThinking != nil {
		if _, ok := body["enable_thinking"]; !ok {
			body["enable_thinking"] = *defaults.EnableThinking
		}
	}
	if defaults.ThinkingBudget > 0 {
		if _, ok := body["thinking"]; !ok {
			body["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": defaults.ThinkingBudget,
			}
		}
	}
}

func (p *Proxy) tryKeys(pv *provider.Provider, path string, reqBody []byte, r *http.Request) (*http.Response, int, []byte, bool) {
	maxAttempts := pv.Keys.AliveCount()
	if maxAttempts == 0 {
		return nil, 0, nil, false
	}

	var lastStatus int
	var lastErrBody []byte

	for attempt := 0; attempt < maxAttempts; attempt++ {
		key, ok := pv.Keys.Next()
		if !ok {
			break
		}

		for retry := 0; retry < maxRetries; retry++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, pv.BaseURL+path+pv.Query, bytes.NewReader(reqBody))
			if err != nil {
				http.Error(nil, "failed to build request: "+err.Error(), http.StatusInternalServerError)
				return nil, 0, nil, true
			}
			for k, v := range pv.Headers {
				req.Header[k] = []string{v}
			}
			switch pv.AuthMode {
			case "both":
				req.Header["Authorization"] = []string{"Bearer " + key}
				req.Header["x-api-key"] = []string{key}
			case "x-api-key":
				req.Header["x-api-key"] = []string{key}
			default:
				req.Header.Set("Authorization", "Bearer "+key)
			}

			r2, err := p.client.Do(req)
			if err != nil {
				p.log.Printf("proxy error via %s (retry %d/%d): %v", pv.Name, retry+1, maxRetries, err)
				lastStatus = http.StatusBadGateway
				lastErrBody = []byte(err.Error())
				if retry < maxRetries-1 {
					if p.backoff(r.Context(), retry) {
						return nil, lastStatus, lastErrBody, true
					}
					continue
				}
				break
			}

			if deadStatuses[r2.StatusCode] {
				eb, _ := io.ReadAll(r2.Body)
				r2.Body.Close()
				p.log.Printf("key %s dead (HTTP %d) via %s, switching...", maskKey(key), r2.StatusCode, pv.Name)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				pv.Keys.MarkDead(key)
				break
			}

			if transientStatuses[r2.StatusCode] {
				eb, _ := io.ReadAll(r2.Body)
				r2.Body.Close()
				p.log.Printf("upstream %s transient (HTTP %d), retry %d/%d", pv.Name, r2.StatusCode, retry+1, maxRetries)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				if retry < maxRetries-1 {
					if p.backoff(r.Context(), retry) {
						return nil, lastStatus, lastErrBody, true
					}
					continue
				}
				break
			}

			return r2, 0, nil, false
		}
	}

	return nil, lastStatus, lastErrBody, false
}

func maskKey(value string) string {
	if len(value) <= 4 {
		return "..."
	}
	return "..." + value[len(value)-4:]
}

func (p *Proxy) backoff(ctx context.Context, retry int) bool {
	d := time.Duration(100*(1<<retry)) * time.Millisecond
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return false
	case <-ctx.Done():
		return true
	}
}

func serveOpenAI(resp *http.Response, clientStream bool, w http.ResponseWriter) {
	if clientStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if err := util.StreamSSE(resp.Body, w, true); err != nil {
			log.Printf("stream error: %v", err)
		}
		return
	}
	result := bufferStream(resp.Body)
	writeJSON(w, http.StatusOK, result)
}

func serveResponses(resp *http.Response, clientStream bool, w http.ResponseWriter) {
	if clientStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if err := util.StreamSSE(resp.Body, w, false); err != nil {
			log.Printf("responses stream error: %v", err)
		}
		return
	}
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (p *Proxy) serveAnthropic(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter) {
	if clientStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamAnthropicToOpenAI(resp.Body, w, modelName); err != nil {
			p.log.Printf("anthropic stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferAnthropicToOpenAI(resp.Body, modelName)
	if err != nil {
		http.Error(w, "failed to translate response: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(openaiBody)
}

func bufferStream(body io.Reader) *model.ChatCompletionResponse {
	content := make(map[int]string)
	reasoning := make(map[int]string)
	finish := make(map[int]string)
	var usage json.RawMessage
	var resultID, modelName, systemFP string
	var created int64
	sawMeta := false

	_, _ = util.IterDataLines(body, func(payload string) bool {
		var chunk model.StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return true
		}
		if !sawMeta {
			resultID = chunk.ID
			modelName = chunk.Model
			systemFP = chunk.SystemFingerprint
			created = chunk.Created
			sawMeta = true
		}
		if len(chunk.Usage) > 0 {
			usage = chunk.Usage
		}
		for _, c := range chunk.Choices {
			idx := c.Index
			if c.Delta.Content != "" {
				content[idx] += c.Delta.Content
			}
			if c.Delta.ReasoningContent != "" {
				reasoning[idx] += c.Delta.ReasoningContent
			}
			if c.FinishReason != nil {
				finish[idx] = *c.FinishReason
			}
		}
		return true
	})

	if !sawMeta {
		return &model.ChatCompletionResponse{
			Object:  "chat.completion",
			Choices: []model.Choice{},
		}
	}

	indices := make(map[int]bool)
	for i := range content {
		indices[i] = true
	}
	for i := range finish {
		indices[i] = true
	}
	keysSorted := make([]int, 0, len(indices))
	for i := range indices {
		keysSorted = append(keysSorted, i)
	}
	sort.Ints(keysSorted)

	choices := make([]model.Choice, 0, len(keysSorted))
	for _, idx := range keysSorted {
		msg := model.Message{Role: "assistant", Content: content[idx]}
		if r := reasoning[idx]; r != "" {
			msg.ReasoningContent = r
		}
		fr := finish[idx]
		if fr == "" {
			fr = "stop"
		}
		choices = append(choices, model.Choice{
			Index:        idx,
			Message:      msg,
			FinishReason: fr,
		})
	}

	return &model.ChatCompletionResponse{
		ID:                resultID,
		Object:            "chat.completion",
		Created:           created,
		Model:             modelName,
		SystemFingerprint: systemFP,
		Choices:           choices,
		Usage:             usage,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}
