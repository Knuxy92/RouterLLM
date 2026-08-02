package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"routerllm/internal/adapter"
	"routerllm/internal/cline"
	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/util"
)

const maxRetries = 3
const autoModelCacheTTL = 30 * time.Minute
const autoModelCacheSize = 1000

var deadStatuses = map[int]bool{
	401: true, 402: true, 403: true,
}

var transientStatuses = map[int]bool{
	408: true, 429: true, 500: true, 502: true, 503: true, 504: true,
}

type Proxy struct {
	registry      *provider.Registry
	client        *http.Client
	log           *log.Logger
	debug         bool
	advancedDebug bool
	forceStream   bool
	systemPrompt  string
	autoModel     config.AutoModelConfig
	autoCacheMu   sync.Mutex
	autoCache     map[string]autoModelCacheEntry
	clineMu       sync.Mutex
	clineManagers map[string]*cline.Manager
}

type autoModelCacheEntry struct {
	result    autoModelResult
	expiresAt time.Time
}

type upstreamCall struct {
	method      string
	path        string
	body        []byte
	contentType string
	sessionID   string
}

func (p *Proxy) clineAccessToken(ctx context.Context, baseURL, refreshToken string, force bool) (string, error) {
	p.clineMu.Lock()
	manager, ok := p.clineManagers[baseURL]
	if !ok {
		store, err := cline.LoadAccountStore(cline.DefaultAccountsPath())
		if err != nil {
			p.log.Printf("cline accounts unavailable: %v", err)
			store = nil
		}
		manager = cline.NewManager(&cline.Client{
			HTTPClient: p.client,
			Endpoints:  cline.Endpoints{Refresh: baseURL + "/v1/auth/refresh"},
		}, store)
		p.clineManagers[baseURL] = manager
	}
	p.clineMu.Unlock()

	return manager.AccessToken(ctx, refreshToken, force)
}

func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger, debug bool, advancedDebug bool, forceStream bool, systemPrompt string, autoModel config.AutoModelConfig) *Proxy {
	return &Proxy{registry: reg, client: client, log: log, debug: debug, advancedDebug: advancedDebug, forceStream: forceStream, systemPrompt: systemPrompt, autoModel: autoModel, autoCache: make(map[string]autoModelCacheEntry), clineManagers: make(map[string]*cline.Manager)}
}

func (p *Proxy) Forward(path string, w http.ResponseWriter, r *http.Request) {
	p.forward(path, w, r, p.forceStream)
}

func (p *Proxy) ForwardFile(w http.ResponseWriter, r *http.Request) {
	modelName := r.URL.Query().Get("model")
	if modelName == "" {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "model query parameter is required")
		return
	}
	routes := p.registry.Routes(modelName)
	if len(routes) == 0 {
		util.WriteError(w, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q not found", modelName))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResolvedMediaSize))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to read file request: "+err.Error())
		return
	}
	path := r.URL.Path
	for _, route := range routes {
		if route.Provider.Style != "openai" {
			continue
		}
		resp, status, errBody, served := p.tryKeys(route.Provider, upstreamCall{
			method:      r.Method,
			path:        path,
			body:        body,
			contentType: r.Header.Get("Content-Type"),
		}, r)

		if served {
			return
		}
		if resp == nil {
			p.logErr("file upstream failed", status, errBody)
			continue
		}
		defer resp.Body.Close()
		copyResponse(w, resp)
		return
	}
	util.WriteError(w, http.StatusBadGateway, "unsupported_file", "no OpenAI-compatible file route is configured")
}

// ForwardRaw does routing, default injection, and upstream call.
// Returns the upstream *http.Response even for non-2xx — the caller must check
// resp.StatusCode. The caller MUST close resp.Body when non-nil.
// Returns (resp, route, nil) when any upstream responds (even non-2xx).
// Returns (nil, nil, error) for pre-request failures (model not found,
// keys exhausted, cancelled).
func (p *Proxy) ForwardRaw(path string, r *http.Request, body map[string]any) (*http.Response, *provider.Route, error) {
	p.injectSystemPrompt(body)

	modelName, _ := body["model"].(string)
	if modelName == "auto" {
		chatID := chatIdentifier(r, body)
		result, cached := p.resolveAutoModel(r.Context(), body, chatID)
		if result.err != nil {
			p.log.Printf("auto-model failed: error=%v request_id=%s chat_id=%s", result.err, requestID(r), chatID)
		} else if cached {
			p.log.Printf("auto-model cache hit: model=%s category=%s request_id=%s chat_id=%s", result.model, result.category, requestID(r), chatID)
		} else if p.advancedDebug {
			p.log.Printf("auto-model selected: %s from row %s raw_content=%q request_id=%s chat_id=%s", result.model, result.category, result.rawContent, requestID(r), chatID)
		} else {
			p.log.Printf("auto-model selected: %s from row %s request_id=%s chat_id=%s", result.model, result.category, requestID(r), chatID)
		}
		resolvedModel := result.model
		err := result.err
		if err != nil {
			return nil, nil, err
		}
		modelName = resolvedModel
		body["model"] = modelName
	}
	routes := p.registry.Routes(modelName)
	if len(routes) == 0 {
		return nil, nil, fmt.Errorf("model %q not found", modelName)
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
			return nil, nil, fmt.Errorf("model %q not found for %s", modelName, path)
		}
	}

	p.log.Printf("%s model=%s routes=%d request_id=%s", path, modelName, len(routes), requestID(r))

	var lastResp *http.Response
	var lastRoute *provider.Route
	var lastErr error

	for _, route := range routes {
		pv := route.Provider
		routeBody := cloneBody(body)
		applyDefaults(routeBody, route.Defaults)

		var reqBody []byte
		var reqPath string
		var sessionID string
		var err error

		if pv.Style == "anthropic" {
			reqBody, reqPath, err = adapter.TranslateRequestWithResolver(routeBody, route.ModelName, p.mediaResolver(pv))
		} else {
			routeBody["model"] = route.ModelName
			if pv.Style == "cline" {
				sessionID = cline.PrepareBody(routeBody)
			}
			reqBody, err = json.Marshal(routeBody)
			reqPath = path
		}

		if err != nil {
			lastErr = fmt.Errorf("failed to encode body for %s: %w", pv.Name, err)
			continue
		}

		resp, status, errBody, served := p.tryKeys(pv, upstreamCall{
			method:      http.MethodPost,
			path:        reqPath,
			body:        reqBody,
			contentType: "application/json",
			sessionID:   sessionID,
		}, r)
		if served {
			if lastResp != nil {
				lastResp.Body.Close()
			}
			return nil, nil, fmt.Errorf("request cancelled")
		}
		if resp == nil {
			p.logErr(fmt.Sprintf("all keys exhausted for %s via %s", modelName, pv.Name), status, errBody)
			lastErr = fmt.Errorf("all keys exhausted for %s via %s (status=%d)", modelName, pv.Name, status)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(eb))
			if lastResp != nil {
				lastResp.Body.Close()
			}
			lastResp = resp
			lastRoute = &route
			p.logResp(fmt.Sprintf("upstream %s returned non-200", pv.Name), resp, eb)
			lastErr = fmt.Errorf("upstream %s returned status %d: %s", pv.Name, resp.StatusCode, briefBody(eb))
			continue
		}

		if lastResp != nil {
			lastResp.Body.Close()
		}
		p.log.Printf("serving %s via provider=%s upstream_model=%s: %s request_id=%s", path, pv.Name, route.ModelName, respSummary(resp), requestID(r))
		return resp, &route, nil
	}

	if lastResp != nil {
		return lastResp, lastRoute, lastErr
	}
	return nil, nil, lastErr
}

type autoModelResolution struct {
	autoModelResult
	err error
}

func (p *Proxy) resolveAutoModel(ctx context.Context, body map[string]any, chatID string) (autoModelResolution, bool) {
	if chatID != "" {
		p.autoCacheMu.Lock()
		entry, ok := p.autoCache[chatID]
		if ok && time.Now().Before(entry.expiresAt) {
			p.autoCacheMu.Unlock()
			return autoModelResolution{autoModelResult: entry.result}, true
		}
		if ok {
			delete(p.autoCache, chatID)
		}
		p.autoCacheMu.Unlock()
	}

	result, err := classifyAutoModel(ctx, p.client, p.autoModel, body)
	if err == nil && chatID != "" {
		p.autoCacheMu.Lock()
		if len(p.autoCache) >= autoModelCacheSize {
			for key := range p.autoCache {
				delete(p.autoCache, key)
				break
			}
		}
		p.autoCache[chatID] = autoModelCacheEntry{result: result, expiresAt: time.Now().Add(autoModelCacheTTL)}
		p.autoCacheMu.Unlock()
	}

	return autoModelResolution{autoModelResult: result, err: err}, false
}

func requestID(r *http.Request) string {
	return r.Header.Get("X-Request-ID")
}

func chatIdentifier(r *http.Request, body map[string]any) string {
	for _, name := range []string{"X-Chat-ID", "X-Conversation-ID"} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}

	if metadata, ok := body["metadata"].(map[string]any); ok {
		if value, ok := metadata["chat_id"].(string); ok {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func (p *Proxy) forward(path string, w http.ResponseWriter, r *http.Request, forceStream bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to read request body: "+err.Error())
		return
	}

	body, clientStream, err := parseAndForceStream(raw)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return
	}

	if path == "/v1/responses" {
		body["stream"] = clientStream
	}

	resp, route, err := p.ForwardRaw(path, r, body)
	if resp != nil {
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
			util.WriteUpstreamError(w, resp.StatusCode, eb)
			return
		}

		if forceStream && path == "/v1/chat/completions" {
			p.serveForceStream(resp, route.ModelName, route.Provider.Style, w)
			return
		}

		if route.Provider.Style == "anthropic" {
			p.serveAnthropic(resp, clientStream, route.ModelName, w)
		} else if path == "/v1/responses" {
			serveResponses(resp, clientStream, w)
		} else {
			serveOpenAI(resp, clientStream, w)
		}
		return
	}

	if err != nil {
		if errors.Is(err, ErrAutoModelDisabled) {
			util.WriteError(w, http.StatusBadRequest, "model_disabled", err.Error())
		} else if strings.Contains(err.Error(), "not found") {
			util.WriteError(w, http.StatusNotFound, "model_not_found", err.Error())
		} else if strings.Contains(err.Error(), "request cancelled") {
			return
		} else {
			util.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		}
		return
	}
}

func parseAndForceStream(raw []byte) (map[string]any, bool, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	if body == nil {
		return nil, false, errors.New("body must be a JSON object")
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
				body["thinking"] = map[string]any{
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
		if existing, ok := body["thinking"].(map[string]any); ok {
			if _, ok := existing["budget_tokens"]; !ok {
				existing["budget_tokens"] = defaults.ThinkingBudget
			}
		} else {
			body["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": defaults.ThinkingBudget,
			}
		}
	}
}

func (p *Proxy) tryKeys(pv *provider.Provider, call upstreamCall, r *http.Request) (*http.Response, int, []byte, bool) {
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
		forceRefresh := false
		refreshed := false

		for retry := 0; retry < maxRetries; retry++ {
			req, err := http.NewRequestWithContext(r.Context(), call.method, pv.BaseURL+call.path+pv.Query, bytes.NewReader(call.body))
			if err != nil {
				p.log.Printf("failed to build request for %s: %v", pv.Name, err)
				lastStatus = http.StatusInternalServerError
				lastErrBody = []byte(err.Error())
				break
			}
			for k, v := range pv.Headers {
				req.Header[k] = []string{v}
			}
			if req.Header.Get("Content-Type") == "" && call.contentType != "" {
				req.Header.Set("Content-Type", call.contentType)
			}

			if pv.Style == "cline" {
				token, err := p.clineAccessToken(r.Context(), pv.BaseURL, key, forceRefresh)
				if err != nil {
					p.log.Printf("cline token refresh failed via %s: %v", pv.Name, err)
					lastStatus = http.StatusUnauthorized
					lastErrBody = []byte(err.Error())
					pv.Keys.MarkDead(key)
					break
				}
				cline.SetHeaders(req.Header, token, call.sessionID)
			} else {
				switch pv.AuthMode {
				case "both":
					req.Header["Authorization"] = []string{"Bearer " + key}
					req.Header["x-api-key"] = []string{key}
				case "x-api-key":
					req.Header["x-api-key"] = []string{key}
				default:
					req.Header.Set("Authorization", "Bearer "+key)
				}
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

			if pv.Style == "cline" && r2.StatusCode == http.StatusUnauthorized && !refreshed {
				eb, _ := io.ReadAll(r2.Body)
				r2.Body.Close()
				p.logResp(fmt.Sprintf("cline token stale via %s, refreshing", pv.Name), r2, eb)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				forceRefresh = true
				refreshed = true
				continue
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
	if p.advancedDebug {
		p.log.Printf("%s: %s body=%s", msg, respSummary(r), briefBody(body))
	} else {
		p.log.Printf("%s: %s", msg, respSummary(r))
	}
}

func (p *Proxy) logErr(msg string, status int, body []byte) {
	if p.advancedDebug {
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

func writeStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func serveOpenAI(resp *http.Response, clientStream bool, w http.ResponseWriter) {
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response: "+err.Error())
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
		if err := util.StreamRawSSE(resp.Body, w); err != nil {
			p.log.Printf("anthropic passthrough stream error: %v", err)
		}
		return
	}

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
	if p.advancedDebug {
		p.log.Printf("injected system prompt: len=%d chars, messages=%d", len(p.systemPrompt), len(body["messages"].([]any)))
	}
}
