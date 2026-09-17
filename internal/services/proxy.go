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
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"os"
	"routerllm/internal/adapter"
	"routerllm/internal/cline"
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/telemetry"
	"routerllm/internal/util"
)

const maxRetries = 3

// The alysis gateway enforces OpenAI's 128-tool cap and rejects larger
// requests with a generic invalid-request error, so the guard fires before
// the upstream call and the client gets an actionable 400 instead.
const maxUpstreamTools = 128

var errTooManyTools = errors.New("too many tools")

var deadStatuses = map[int]bool{
	401: true, 402: true, 403: true,
}

var transientStatuses = map[int]bool{
	408: true, 429: true, 500: true, 502: true, 503: true, 504: true,
}

var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"content-length":      true,
	"host":                true,
	"keep-alive":          true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// clientCredentialHeaders never reach an upstream, even when client header
// forwarding is on and the allowlist names them: the router picks the upstream
// credential, and a client's own credentials or session cookies must not ride
// along to a third-party provider.
var clientCredentialHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"x-goog-api-key":      true,
	"cookie":              true,
	"cookie2":             true,
	"proxy-authorization": true,
	"accept-encoding":     true,
}

type Proxy struct {
	registry             atomic.Pointer[provider.Registry]
	client               *http.Client
	log                  *log.Logger
	debug                bool
	advancedDebug        bool
	forceStream          atomic.Bool
	forceStreamUsage     bool
	dedupeTools          atomic.Bool
	forwardClientHeaders atomic.Bool
	allowClientHeaders   atomic.Pointer[[]string]
	systemPrompt         atomic.Pointer[string]
	clineMu              sync.Mutex
	clineManagers        map[string]*cline.Manager
	telemetry            atomic.Pointer[telemetry.Store]
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

// debug controls the per-request routing trace (which model, which provider
// served it). advancedDebug additionally logs response bodies. Failure lines —
// dead keys, retries, exhausted providers — are always logged regardless, since
// they are what an operator needs when a provider breaks.
func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger, debug bool, advancedDebug bool, forceStream bool, forwardClientHeaders bool, allowClientHeaders []string, systemPrompt string) *Proxy {
	p := &Proxy{client: client, log: log, debug: debug, advancedDebug: advancedDebug, forceStreamUsage: os.Getenv("ROUTERLLM_TELEMETRY_USAGE") != "off", clineManagers: make(map[string]*cline.Manager)}
	p.forceStream.Store(forceStream)
	p.forwardClientHeaders.Store(forwardClientHeaders)
	p.storeAllowClientHeaders(allowClientHeaders)
	p.telemetry.Store(telemetry.NewMemStore())
	p.registry.Store(reg)
	p.systemPrompt.Store(&systemPrompt)

	return p
}

// SetTelemetry attaches the request-event sink. Passing nil attaches an
// in-memory no-persist store so recording stays nil-safe everywhere.
func (p *Proxy) SetTelemetry(s *telemetry.Store) {
	if s == nil {
		s = telemetry.NewMemStore()
	}
	p.telemetry.Store(s)
}

// TelemetryStore exposes the event sink so handlers outside services can read
// buffered events and metrics.
func (p *Proxy) TelemetryStore() *telemetry.Store {
	return p.telemetry.Load()
}

// recordTelemetry fills the common fields and hands the event to the store.
func (p *Proxy) recordTelemetry(e telemetry.Event) {
	p.telemetry.Load().Record(e)
}

// RecordTelemetry hands a finished event to the store. Handlers outside
// services (e.g. /v1/messages) use it to close out a RequestTrace they started
// with WithRequestTrace.
func (p *Proxy) RecordTelemetry(e telemetry.Event) {
	p.recordTelemetry(e)
}

func (p *Proxy) Registry() *provider.Registry {
	return p.registry.Load()
}

// Apply swaps in a rebuilt registry. Cline managers are keyed by provider
// base URL, so any manager whose URL is no longer configured is dropped here —
// otherwise it would retain its account store and refresh-token cache for the
// process lifetime.
func (p *Proxy) Apply(reg *provider.Registry, systemPrompt string) {
	p.registry.Store(reg)
	p.systemPrompt.Store(&systemPrompt)
	p.pruneClineManagers(reg)
}

// ApplySettings swaps the request-shaping options that hot reload can change.
// The allowlist is copied so a caller's slice cannot be mutated under a live
// request; the values are read per request, so a swap applies at once.
func (p *Proxy) ApplySettings(forceStream, forwardClientHeaders bool, allowClientHeaders []string) {
	p.forceStream.Store(forceStream)
	p.forwardClientHeaders.Store(forwardClientHeaders)
	p.storeAllowClientHeaders(allowClientHeaders)
}

// SetDedupeTools turns the global tool-dedupe switch on or off. A leg's own
// dedupe_tools still works when this is off; when this is on, every leg
// dedupes.
func (p *Proxy) SetDedupeTools(enabled bool) {
	p.dedupeTools.Store(enabled)
}

func (p *Proxy) storeAllowClientHeaders(allow []string) {
	clone := append([]string(nil), allow...)
	p.allowClientHeaders.Store(&clone)
}

// clientHeaderAllowlist returns the configured allowlist, or nil when none was
// stored — both mean "forward everything not denied".
func (p *Proxy) clientHeaderAllowlist() []string {
	if allow := p.allowClientHeaders.Load(); allow != nil {
		return *allow
	}

	return nil
}

func (p *Proxy) pruneClineManagers(reg *provider.Registry) {
	live := make(map[string]bool)
	for _, pc := range reg.ProviderConfigs() {
		if pc.Style == "cline" && !pc.Disabled {
			live[pc.BaseURL] = true
		}
	}

	p.clineMu.Lock()
	defer p.clineMu.Unlock()

	for baseURL := range p.clineManagers {
		if !live[baseURL] {
			delete(p.clineManagers, baseURL)
		}
	}
}

func (p *Proxy) Models() []string {
	return p.registry.Load().AllModels()
}

func copyClientHeaders(dst, src *http.Request, allow []string) {
	allowed := make(map[string]bool, len(allow))
	for _, h := range allow {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for key, values := range src.Header {
		lk := strings.ToLower(key)
		if hopByHopHeaders[lk] || clientCredentialHeaders[lk] {
			continue
		}
		if len(allow) > 0 && !allowed[lk] {
			continue
		}
		dst.Header[key] = append([]string(nil), values...)
	}
}

func (p *Proxy) Forward(path string, w http.ResponseWriter, r *http.Request) {
	p.forward(path, w, r, p.forceStream.Load())
}

func (p *Proxy) ForwardFile(w http.ResponseWriter, r *http.Request) {
	modelName := r.URL.Query().Get("model")
	if modelName == "" {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "model query parameter is required")
		return
	}
	routes := p.registry.Load().Routes(modelName)
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
func (p *Proxy) ForwardRaw(path string, r *http.Request, body map[string]any) (*http.Response, *provider.Route, map[string]string, error) {
	p.injectSystemPrompt(body)

	modelName, _ := body["model"].(string)
	routes := p.registry.Load().Routes(modelName)
	if len(routes) == 0 {
		return nil, nil, nil, fmt.Errorf("model %q not found", modelName)
	}

	if path != "/v1/chat/completions" {
		var filtered []provider.Route
		for _, r := range routes {
			if d := r.Dialect(); d == "openai" || d == "responses" {
				filtered = append(filtered, r)
			}
		}
		routes = filtered
		if len(routes) == 0 {
			return nil, nil, nil, fmt.Errorf("model %q not found for %s", modelName, path)
		}
	}

	if p.debug {
		p.log.Printf("%s model=%s routes=%d request_id=%s", path, modelName, len(routes), requestID(r))
	}

	started := time.Now()
	trace := traceFromContext(r.Context())
	var attempts []telemetry.Attempt

	var lastResp *http.Response
	var lastRoute *provider.Route
	var lastRestore map[string]string
	var lastErr error

	for _, route := range routes {
		pv := route.Provider
		routeBody := cloneBody(body)

		reqBody, reqPath, sessionID, toolNameRestore, err := p.translateRoute(pv, route, path, routeBody)
		if err != nil {
			if !errors.Is(err, errTooManyTools) {
				err = fmt.Errorf("failed to encode body for %s: %w", pv.Name, err)
			}
			lastErr = err
			attempts = append(attempts, telemetry.Attempt{Provider: pv.Name, Model: route.ModelName, Status: 0, Note: "encode error"})
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
			return nil, nil, nil, fmt.Errorf("request cancelled")
		}
		if resp == nil {
			p.logErr(fmt.Sprintf("all keys exhausted for %s via %s", modelName, pv.Name), status, errBody)
			lastErr = fmt.Errorf("all keys exhausted for %s via %s (status=%d)", modelName, pv.Name, status)
			attempt := telemetry.Attempt{Provider: pv.Name, Model: route.ModelName, Status: status, Note: "all keys exhausted", RespBody: telemetry.ClampBody(errBody, telemetry.RespBodyCap)}
			if trace != nil && attempt.RespBody != "" {
				trace.setLastBody(attempt.RespBody)
			}
			attempts = append(attempts, attempt)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			eb := ReadErrorBody(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(eb))
			if lastResp != nil {
				lastResp.Body.Close()
			}
			lastResp = resp
			lastRoute = &route
			lastRestore = toolNameRestore
			p.logResp(fmt.Sprintf("upstream %s returned non-200", pv.Name), resp, eb)
			lastErr = fmt.Errorf("upstream %s returned status %d: %s", pv.Name, resp.StatusCode, briefBody(eb))
			attempt := telemetry.Attempt{Provider: pv.Name, Model: route.ModelName, Status: resp.StatusCode, LatencyMS: time.Since(started).Milliseconds(), Note: "non-200", RespBody: telemetry.ClampBody(eb, telemetry.RespBodyCap)}
			if trace != nil && attempt.RespBody != "" {
				trace.setLastBody(attempt.RespBody)
			}
			attempts = append(attempts, attempt)
			continue
		}

		if lastResp != nil {
			lastResp.Body.Close()
		}
		if p.debug {
			p.log.Printf("serving %s via provider=%s upstream_model=%s dialect=%s reasoning=[%s]: %s request_id=%s", path, pv.Name, route.ModelName, route.Dialect(), reasoningSummary(routeBody), respSummary(resp), requestID(r))
		}

		if trace != nil {
			trace.setServed(pv.Name, route.ModelName, maskKey(servedKey(resp, pv)))
			trace.attempts = attempts
		}
		resp.Body = telemetry.Watch(resp.Body)
		if trace != nil {
			trace.ttftMS = time.Since(started).Milliseconds()
		}
		return resp, &route, toolNameRestore, nil
	}

	if trace != nil {
		trace.attempts = attempts
	}
	if lastResp != nil {
		return lastResp, lastRoute, lastRestore, lastErr
	}
	return nil, nil, nil, lastErr
}

// translateRoute builds the outbound body and request path for one route leg:
// it folds the route defaults and the provider's reasoning dialect into
// routeBody (mutated in place) and translates it into the leg's wire dialect
// (the explicit stylecall when set, otherwise the provider style's native
// dialect). Shared by ForwardRaw and the admin test runner. The returned
// reverse map restores sanitized tool names on the way back; it is nil when the
// leg leaves tool names untouched.
func (p *Proxy) translateRoute(pv *provider.Provider, route provider.Route, path string, routeBody map[string]any) (reqBody []byte, reqPath string, sessionID string, toolNameRestore map[string]string, err error) {
	if dedupe := route.DedupeTools || p.dedupeTools.Load(); dedupe || route.SanitizeToolNames {
		restore, dropped := processToolNames(routeBody, dedupe, route.SanitizeToolNames)
		if dropped > 0 {
			p.log.Printf("route %s/%s: dropped %d duplicate tool definition(s)", route.ModelName, pv.Name, dropped)
		}
		if route.SanitizeToolNames {
			toolNameRestore = restore
		}
	}

	// The alysis gateway enforces OpenAI's 128-tool cap regardless of which
	// dialect the leg is emitted in, so the guard runs before the dispatch.
	if pv.Style == "alysis" {
		if tools, ok := routeBody["tools"].([]any); ok && len(tools) > maxUpstreamTools {
			return nil, "", "", nil, fmt.Errorf("%w: %d tools exceeds the alysis gateway limit of %d (provider %s) — disable some MCP servers or route the model elsewhere", errTooManyTools, len(tools), maxUpstreamTools, pv.Name)
		}
	}

	switch route.Dialect() {
	case "messages":
		applyCanonicalDefaults(routeBody, route.Defaults)
		reqBody, reqPath, err = adapter.TranslateRequestWithResolver(routeBody, route.ModelName, p.mediaResolver(pv))
	case "google":
		applyCanonicalDefaults(routeBody, route.Defaults)
		reqBody, reqPath, err = adapter.TranslateGoogleRequestWithResolver(routeBody, route.ModelName, p.mediaResolverNoAuth(pv))
	case "cline":
		applyCanonicalDefaults(routeBody, route.Defaults)
		delete(routeBody, "thinking_budget")
		delete(routeBody, "reasoning_exclude")
		routeBody["model"] = route.ModelName
		sessionID = cline.PrepareBody(routeBody)
		p.injectStreamUsage(routeBody, path)
		reqBody, err = json.Marshal(routeBody)
		reqPath = path
	case "chat":
		routeBody["model"] = route.ModelName
		if pv.ReasoningStyle == "raw" {
			applyLegacyDefaults(routeBody, route.Defaults)
		} else {
			if notice := canonicalizeReasoning(routeBody, route.Defaults); notice != "" {
				p.log.Printf("route %s/%s: %s — ignored", route.ModelName, pv.Name, notice)
			}
			applyReasoningDialect(routeBody, pv.ReasoningStyle)
		}
		p.injectStreamUsage(routeBody, path)
		reqBody, err = json.Marshal(routeBody)
		reqPath = "/v1/chat/completions"
	case "responses":
		if path == "/v1/responses" {
			// Inbound is already Responses-shaped: passthrough with the
			// upstream model substituted.
			routeBody["model"] = route.ModelName
			applyLegacyDefaults(routeBody, route.Defaults)
			reqBody, err = json.Marshal(routeBody)
			reqPath = path
		} else {
			applyCanonicalDefaults(routeBody, route.Defaults)
			reqBody, reqPath, err = adapter.TranslateResponsesRequest(routeBody, route.ModelName)
		}
	default:
		routeBody["model"] = route.ModelName
		if pv.ReasoningStyle == "raw" || path != "/v1/chat/completions" {
			applyLegacyDefaults(routeBody, route.Defaults)
		} else {
			if notice := canonicalizeReasoning(routeBody, route.Defaults); notice != "" {
				p.log.Printf("route %s/%s: %s — ignored", route.ModelName, pv.Name, notice)
			}
			applyReasoningDialect(routeBody, pv.ReasoningStyle)
		}
		p.injectStreamUsage(routeBody, path)
		reqBody, err = json.Marshal(routeBody)
		reqPath = path
	}

	return reqBody, reqPath, sessionID, toolNameRestore, err
}

// servedKey extracts the credential actually used from the completed request
// so telemetry can store its mask. The style decides which header carries it.
func servedKey(resp *http.Response, pv *provider.Provider) string {
	if resp.Request == nil {
		return ""
	}
	if pv.Style == "google" {
		return resp.Request.Header.Get("x-goog-api-key")
	}
	if key := strings.TrimPrefix(resp.Request.Header.Get("Authorization"), "Bearer "); key != "" && key != resp.Request.Header.Get("Authorization") {
		return key
	}

	return resp.Request.Header.Get("x-api-key")
}

func requestID(r *http.Request) string {
	return r.Header.Get("X-Request-ID")
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

	ctx, trace := WithRequestTrace(r.Context())
	r = r.WithContext(ctx)
	modelName, _ := body["model"].(string)
	reqID := requestID(r)

	resp, route, toolNameRestore, err := p.ForwardRaw(path, r, body)
	if resp != nil {
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			eb := ReadErrorBody(resp.Body)
			util.WriteUpstreamError(w, resp.StatusCode, TruncateErrorBody(eb))
			p.recordTelemetry(trace.Event(modelName, reqID, resp.StatusCode, briefBody(eb), 0, telemetry.ClampBody(eb, telemetry.RespBodyCap)))
			return
		}

		d := route.Dialect()
		if forceStream && path == "/v1/chat/completions" {
			p.serveForceStream(resp, route.ModelName, d, w, toolNameRestore)
			p.recordTelemetry(trace.Event(modelName, reqID, http.StatusOK, "", traceTokens(resp), ""))
			return
		}

		switch d {
		case "messages":
			p.serveAnthropic(resp, clientStream, route.ModelName, w)
		case "google":
			p.serveGoogle(resp, clientStream, route.ModelName, w)
		case "responses":
			if path == "/v1/responses" {
				serveResponses(resp, clientStream, w)
			} else {
				p.serveResponsesAsChat(resp, clientStream, route.ModelName, w, toolNameRestore)
			}
		default:
			if path == "/v1/responses" {
				serveResponses(resp, clientStream, w)
			} else {
				serveOpenAI(resp, clientStream, w, toolNameRestore)
			}
		}
		p.recordTelemetry(trace.Event(modelName, reqID, http.StatusOK, "", traceTokens(resp), ""))
		return
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			util.WriteError(w, http.StatusNotFound, "model_not_found", err.Error())
			p.recordTelemetry(trace.Event(modelName, reqID, http.StatusNotFound, err.Error(), 0, ""))
		} else if strings.Contains(err.Error(), "request cancelled") {
			return
		} else if errors.Is(err, errTooManyTools) {
			util.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
			p.recordTelemetry(trace.Event(modelName, reqID, http.StatusBadRequest, err.Error(), 0, ""))
		} else {
			util.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
			p.recordTelemetry(trace.Event(modelName, reqID, http.StatusBadGateway, err.Error(), 0, trace.LastBody()))
		}
		return
	}
}

// traceTokens reads the completion token count captured by the usage watcher,
// if one was attached to this response body.
func traceTokens(resp *http.Response) int {
	if watcher, ok := resp.Body.(*telemetry.Watcher); ok {
		return watcher.Tokens()
	}

	return 0
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

// applyLegacyDefaults is the pre-normalization injection used by raw-mode
// openai-style providers and the /v1/responses passthrough: it writes the
// request defaults verbatim (including the Anthropic-shaped thinking block)
// without folding client dialects.
func applyLegacyDefaults(body map[string]any, defaults model.RequestDefaults) {
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

// injectStreamUsage asks OpenAI-compatible upstreams for a final usage chunk
// so the telemetry watcher can sniff completion-token counts. A client-sent
// stream_options wins; anthropic/google dialects and /v1/responses never see
// the field, so they are left alone.
func (p *Proxy) injectStreamUsage(routeBody map[string]any, path string) {
	if !p.forceStreamUsage || path != "/v1/chat/completions" {
		return
	}
	if _, ok := routeBody["stream_options"]; ok {
		return
	}

	routeBody["stream_options"] = map[string]any{"include_usage": true}
}

func (p *Proxy) tryKeys(pv *provider.Provider, call upstreamCall, r *http.Request) (*http.Response, int, []byte, bool) {
	maxAttempts := pv.Keys.AliveCount()
	if maxAttempts == 0 {
		return nil, 0, nil, false
	}

	pv.RecordRequest()
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
				safeErr := redactURLError(err)
				p.log.Printf("failed to build request for %s: %s", pv.Name, safeErr)
				lastStatus = http.StatusInternalServerError
				lastErrBody = []byte(safeErr)
				break
			}
			if p.forwardClientHeaders.Load() {
				copyClientHeaders(req, r, p.clientHeaderAllowlist())
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
			} else if pv.Style == "google" {
				req.Header.Set("x-goog-api-key", key)
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
				safeErr := redactURLError(err)
				p.log.Printf("proxy error via %s (retry %d/%d): %s", pv.Name, retry+1, maxRetries, safeErr)
				lastStatus = http.StatusBadGateway
				lastErrBody = []byte(safeErr)
				if retry < maxRetries-1 {
					if p.backoff(r.Context(), retry) {
						return nil, lastStatus, lastErrBody, true
					}
					continue
				}
				break
			}

			if pv.Style == "cline" && r2.StatusCode == http.StatusUnauthorized && !refreshed {
				eb := ReadErrorBody(r2.Body)
				r2.Body.Close()
				p.logResp(fmt.Sprintf("cline token stale via %s, refreshing", pv.Name), r2, eb)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				forceRefresh = true
				refreshed = true
				continue
			}

			if deadStatuses[r2.StatusCode] {
				eb := ReadErrorBody(r2.Body)
				r2.Body.Close()
				p.logResp(fmt.Sprintf("key %s dead via %s", maskKey(key), pv.Name), r2, eb)
				lastStatus = r2.StatusCode
				lastErrBody = eb
				pv.Keys.MarkDead(key)
				break
			}

			if transientStatuses[r2.StatusCode] {
				eb := ReadErrorBody(r2.Body)
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

			if r2.StatusCode >= 400 {
				pv.RecordError()
			}

			return r2, 0, nil, false
		}
	}

	pv.RecordError()

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

const (
	// maxErrorBodyRead bounds how much of an upstream error body is captured.
	maxErrorBodyRead = 64 << 10
	// maxClientErrorBytes bounds the upstream error text echoed to a client.
	maxClientErrorBytes = 16 << 10
)

// ReadErrorBody drains an error response body for logging and telemetry, capped
// so a broken or hostile upstream cannot feed the proxy an unbounded stream.
func ReadErrorBody(r io.Reader) []byte {
	body, _ := io.ReadAll(io.LimitReader(r, maxErrorBodyRead))

	return body
}

// TruncateErrorBody caps the upstream error text echoed back to a client.
func TruncateErrorBody(body []byte) []byte {
	if len(body) <= maxClientErrorBytes {
		return body
	}

	return body[:maxClientErrorBytes]
}

// redactURLError renders a transport error with the outbound query string
// masked: the provider's Query can carry credentials (`?key=...`), which must
// not reach logs, telemetry, or the client. Scheme, host, and path are kept.
func redactURLError(err error) string {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err.Error()
	}

	return fmt.Sprintf("%s %q: %s", urlErr.Op, redactQuery(urlErr.URL), urlErr.Err)
}

func redactQuery(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i] + "?…redacted"
	}

	return rawURL
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

func serveOpenAI(resp *http.Response, clientStream bool, w http.ResponseWriter, toolNameRestore map[string]string) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := util.StreamSSETransform(resp.Body, w, true, streamRestoreTransform(toolNameRestore)); err != nil {
			log.Printf("stream error: %v", err)
		}
		return
	}

	result := bufferStream(resp.Body)
	writeJSON(w, http.StatusOK, restoreBufferedToolNames(result, toolNameRestore))
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

func (p *Proxy) serveGoogle(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamGoogleToOpenAI(resp.Body, w, modelName); err != nil {
			p.log.Printf("google stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferGoogleToOpenAI(resp.Body, modelName)
	if err != nil {
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(openaiBody)
}

// serveResponsesAsChat serves a Responses-dialect leg to a chat-completions
// client: the upstream Responses SSE is converted back into chat chunks or
// buffered into a single chat.completion document.
func (p *Proxy) serveResponsesAsChat(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter, toolNameRestore map[string]string) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamResponsesToOpenAI(resp.Body, newRestoringWriter(w, toolNameRestore), modelName); err != nil {
			p.log.Printf("responses stream error: %v", err)
		}
		return
	}

	data, err := adapter.BufferResponsesToOpenAI(resp.Body, modelName)
	if err != nil {
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response: "+err.Error())
		return
	}

	restored := restoreToolCallNamesJSON(data, toolNameRestore)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(restored)
}

func bufferStream(body io.Reader) *model.ChatCompletionResponse {
	content := make(map[int]string)
	reasoning := make(map[int]string)
	finish := make(map[int]string)
	tools := make(map[int]map[int]*model.ToolCall)

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
			if len(c.Delta.ToolCalls) > 0 {
				calls := tools[idx]
				if calls == nil {
					calls = make(map[int]*model.ToolCall)
					tools[idx] = calls
				}
				for _, fragment := range c.Delta.ToolCalls {
					mergeToolCallFragment(calls, fragment)
				}
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
	for i := range tools {
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
		if calls := tools[idx]; len(calls) > 0 {
			msg.ToolCalls = assembleToolCalls(calls)
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

// mergeToolCallFragment folds one streaming tool_call delta into the
// per-choice accumulator: identity fields are set once, argument fragments
// concatenate in arrival order.
func mergeToolCallFragment(calls map[int]*model.ToolCall, fragment model.ToolCall) {
	existing := calls[fragment.Index]
	if existing == nil {
		existing = &model.ToolCall{Index: fragment.Index}
		calls[fragment.Index] = existing
	}

	if fragment.ID != "" {
		existing.ID = fragment.ID
	}
	if fragment.Type != "" {
		existing.Type = fragment.Type
	}
	if fragment.Function.Name != "" {
		existing.Function.Name = fragment.Function.Name
	}
	existing.Function.Arguments += fragment.Function.Arguments
}

func assembleToolCalls(fragments map[int]*model.ToolCall) []model.ToolCall {
	indexes := make([]int, 0, len(fragments))
	for i := range fragments {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)

	calls := make([]model.ToolCall, 0, len(indexes))
	for _, i := range indexes {
		calls = append(calls, *fragments[i])
	}

	return calls
}

func (p *Proxy) serveForceStream(resp *http.Response, modelName, dialect string, w http.ResponseWriter, toolNameRestore map[string]string) {
	writeStreamHeaders(w)
	w.WriteHeader(http.StatusOK)

	if dialect == "messages" {
		if err := util.StreamRawSSE(resp.Body, w); err != nil {
			p.log.Printf("anthropic passthrough stream error: %v", err)
		}
		return
	}

	if dialect == "google" {
		if err := adapter.StreamGoogleToAnthropicSSE(resp.Body, w, modelName); err != nil {
			p.log.Printf("google stream error: %v", err)
		}
		return
	}

	if dialect == "responses" {
		if err := adapter.StreamResponsesToAnthropicSSE(resp.Body, w, modelName); err != nil {
			p.log.Printf("responses stream error: %v", err)
		}
		return
	}

	adapter.StreamOpenAIToAnthropicSSE(resp.Body, newRestoringWriter(w, toolNameRestore), modelName)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}

func (p *Proxy) injectSystemPrompt(body map[string]any) {
	prompt := *p.systemPrompt.Load()
	if prompt == "" {
		return
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return
	}
	sysMsg := map[string]any{"role": "system", "content": prompt}
	body["messages"] = append([]any{sysMsg}, msgs...)
	if p.advancedDebug {
		p.log.Printf("injected system prompt: len=%d chars, messages=%d", len(prompt), len(body["messages"].([]any)))
	}
}
