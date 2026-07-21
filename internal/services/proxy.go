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
	registry     *provider.Registry
	client       *http.Client
	log          *log.Logger
	debug        bool
	forceStream  bool
	systemPrompt string
}

func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger, debug bool, forceStream bool, systemPrompt string) *Proxy {
	return &Proxy{registry: reg, client: client, log: log, debug: debug, forceStream: forceStream, systemPrompt: systemPrompt}
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

	p.injectSystemPrompt(body)

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

		resp, status, errBody, served := p.tryKeys(pv, http.MethodPost, reqPath, reqBody, r)
		if served {
			return
		}
		if resp == nil {
			p.logErr(fmt.Sprintf("all keys exhausted for %s via %s", model, pv.Name), status, errBody)
			lastStatus = status
			lastErrBody = errBody
			continue
		}

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			p.logResp(fmt.Sprintf("upstream %s returned non-200", pv.Name), resp, eb)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(eb)
			return
		}

		p.log.Printf("serving %s via %s: %s", path, pv.Name, respSummary(resp))

		if p.forceStream && path == "/v1/chat/completions" {
			p.serveForceStream(resp, route.ModelName, pv.Style, w)
			return
		}

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
		p.logErr(fmt.Sprintf("all providers exhausted for %s", model), lastStatus, lastErrBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastStatus)
		w.Write(lastErrBody)
	} else {
		p.log.Printf("all providers exhausted for %s: no routes available", model)
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
		if *defaults.EnableThinking {
			if _, ok := body["enable_thinking"]; !ok {
				body["thinking"] = map[string]string{
					"type": "enabled",
				}
			}
		} else {
			delete(body, "thinking")
			if _, ok := body["enable_thinking"]; !ok {
				body["enable_thinking"] = false
			}
			return
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

func (p *Proxy) tryKeys(pv *provider.Provider, method, path string, reqBody []byte, r *http.Request) (*http.Response, int, []byte, bool) {
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
			req, err := http.NewRequestWithContext(r.Context(), method, pv.BaseURL+path+pv.Query, bytes.NewReader(reqBody))
			if err != nil {
				p.log.Printf("failed to build request for %s: %v", pv.Name, err)
				lastStatus = http.StatusInternalServerError
				lastErrBody = []byte(err.Error())
				break
			}
			for k, v := range pv.Headers {
				req.Header[k] = []string{v}
			}
			if req.Header.Get("Content-Type") == "" {
				req.Header.Set("Content-Type", "application/json")
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
				p.logResp(fmt.Sprintf("key %s dead via %s", maskKey(key), pv.Name), r2, eb)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				pv.Keys.MarkDead(key)
				break
			}

			if transientStatuses[r2.StatusCode] {
				eb, _ := io.ReadAll(r2.Body)
				r2.Body.Close()
				p.logResp(fmt.Sprintf("upstream %s transient (retry %d/%d)", pv.Name, retry+1, maxRetries), r2, eb)
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

func (p *Proxy) logResp(msg string, r *http.Response, body []byte) {
	if p.debug {
		p.log.Printf("%s: %s body=%s", msg, respSummary(r), briefBody(body))
	} else {
		p.log.Printf("%s: %s", msg, respSummary(r))
	}
}

func (p *Proxy) logErr(msg string, status int, body []byte) {
	if p.debug {
		p.log.Printf("%s: status=%d body=%s", msg, status, briefBody(body))
	} else {
		p.log.Printf("%s: status=%d", msg, status)
	}
}

func maskKey(value string) string {
	if len(value) <= 4 {
		return "..."
	}
	return "..." + value[len(value)-4:]
}

func briefBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	s := string(body)
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}

func respSummary(r *http.Response) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("status=%d ct=%s xrid=%s", r.StatusCode, r.Header.Get("Content-Type"), r.Header.Get("x-request-id"))
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

func (p *Proxy) tryKeysMethod(pv *provider.Provider, method, path string, reqBody []byte, r *http.Request) (*http.Response, int, []byte, bool) {
	return p.tryKeys(pv, method, path, reqBody, r)
}

func copyHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}

func writeStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func (p *Proxy) Passthrough(path string, w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	modelName, _ := body["model"].(string)
	routes := p.registry.Routes(modelName)
	if len(routes) == 0 {
		http.Error(w, fmt.Sprintf("model %q not found", modelName), http.StatusNotFound)
		return
	}

	for _, route := range routes {
		pv := route.Provider
		var reqBody []byte
		if len(raw) > 0 {
			body["model"] = route.ModelName
			reqBody, _ = json.Marshal(body)
		}
		method := r.Method
		if method == "" {
			method = http.MethodPost
		}
		resp, _, _, served := p.tryKeysMethod(pv, method, path, reqBody, r)
		if served {
			return
		}
		if resp == nil {
			continue
		}

		p.log.Printf("served %s via %s: %s", path, route.Provider.Name, respSummary(resp))
		copyHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		resp.Body.Close()
		return
	}

	http.Error(w, fmt.Sprintf("all providers exhausted for model %q", modelName), http.StatusBadGateway)
}

func (p *Proxy) PassthroughMultipart(path string, w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	modelName := r.FormValue("model")
	routes := p.registry.Routes(modelName)
	if len(routes) == 0 {
		http.Error(w, fmt.Sprintf("model %q not found", modelName), http.StatusNotFound)
		return
	}

	for _, route := range routes {
		pv := route.Provider
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, pv.BaseURL+path+pv.Query, bytes.NewReader(raw))
		if err != nil {
			continue
		}
		for k, v := range pv.Headers {
			req.Header[k] = []string{v}
		}
		if ct != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", ct)
		}
		switch pv.AuthMode {
		case "both":
			req.Header["Authorization"] = []string{"Bearer " + pv.Keys.LiveKey()}
			req.Header["x-api-key"] = []string{pv.Keys.LiveKey()}
		case "x-api-key":
			req.Header["x-api-key"] = []string{pv.Keys.LiveKey()}
		default:
			req.Header.Set("Authorization", "Bearer "+pv.Keys.LiveKey())
		}

		resp, doErr := p.client.Do(req)
		if doErr != nil {
			p.log.Printf("proxy error via %s: %v", pv.Name, doErr)
			continue
		}

		p.log.Printf("served %s via %s: %s", path, pv.Name, respSummary(resp))
		copyHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		resp.Body.Close()
		return
	}

	http.Error(w, fmt.Sprintf("all providers exhausted for model %q", modelName), http.StatusBadGateway)
}

func serveOpenAI(resp *http.Response, clientStream bool, w http.ResponseWriter) {
	if clientStream {
		writeStreamHeaders(w)
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
		writeStreamHeaders(w)
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
		writeStreamHeaders(w)
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

func (p *Proxy) serveForceStream(resp *http.Response, modelName, style string, w http.ResponseWriter) {
	writeStreamHeaders(w)
	w.WriteHeader(http.StatusOK)

	if style == "anthropic" {
		// Anthropic upstream → passthrough SSE with flush
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
		return
	}

	// OpenAI upstream → convert to Anthropic SSE
	adapter.StreamOpenAIToAnthropicSSE(resp.Body, w, modelName)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}

func (p *Proxy) injectSystemPrompt(body map[string]any) {
	if p.systemPrompt == "" {
		return
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return
	}
	sysMsg := map[string]any{"role": "system", "content": p.systemPrompt}
	body["messages"] = append([]any{sysMsg}, msgs...)
	if p.debug {
		p.log.Printf("injected system prompt: len=%d chars, messages=%d", len(p.systemPrompt), len(body["messages"].([]any)))
	}
}
