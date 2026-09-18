package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"routerllm/internal/adapter"
	"routerllm/internal/provider"
	"routerllm/internal/telemetry"
)

const (
	testDefaultTimeout   = 20 * time.Second
	testDefaultMaxTokens = 256
)

// TestRequest describes one manual provider test run driven from the admin
// console. The handler parses and clamps the raw JSON fields; RunTest consumes
// the typed values.
type TestRequest struct {
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	Prompt    string        `json:"prompt"`
	MaxTokens int           `json:"max_tokens"`
	Effort    string        `json:"effort"`
	StyleCall string        `json:"stylecall"`
	Timeout   time.Duration `json:"timeout_seconds"`
}

// TestResult is the outcome of one test run. Transport-level failures (timeout,
// cancellation) carry Status 0 with the reason in Error; upstream rejections
// carry the upstream status and its clamped body in Error.
type TestResult struct {
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model"`
	Style         string `json:"style"`
	Status        int    `json:"status"`
	TTFTMS        int64  `json:"ttft_ms"`
	DurationMS    int64  `json:"duration_ms"`
	Tokens        int    `json:"tokens"`
	Content       string `json:"content"`
	Error         string `json:"error"`
}

// RunTest drives one manual provider test: it builds a minimal streaming chat
// request, reuses the routing translation for the provider's dialect, calls the
// upstream through the normal key failover, and reports timing, content and
// errors. It never consults routes and never writes to the client.
func (p *Proxy) RunTest(ctx context.Context, req TestRequest) *TestResult {
	start := time.Now()
	result := &TestResult{Provider: req.Provider, UpstreamModel: req.Model}

	pv, status, err := p.testProvider(req.Provider)
	if err != nil {
		result.Status = status
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}

	result.Style = pv.Style

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = testDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	route := provider.Route{Provider: pv, ModelName: req.Model, StyleCall: req.StyleCall}
	reqBody, reqPath, sessionID, _, err := p.translateRoute(pv, route, "/v1/chat/completions", testBody(req))
	if err != nil {
		result.Status = http.StatusBadRequest
		result.Error = "failed to build test request: " + err.Error()
		p.finishTestEvent(start, pv, result, "", "")
		return result
	}

	synthetic, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		result.Status = http.StatusInternalServerError
		result.Error = "failed to build test request: " + err.Error()
		p.finishTestEvent(start, pv, result, "", "")
		return result
	}

	resp, status, errBody, served := p.tryKeys(pv, upstreamCall{
		method:      http.MethodPost,
		path:        reqPath,
		body:        reqBody,
		contentType: "application/json",
		sessionID:   sessionID,
	}, synthetic)

	if served || resp == nil {
		p.finishTestFailure(start, ctx, pv, result, status, errBody, served, timeout)
		return result
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		eb := ReadErrorBody(resp.Body)
		clamped := telemetry.ClampBody(eb, telemetry.RespBodyCap)
		result.Status = resp.StatusCode
		result.Error = fmt.Sprintf("upstream %s returned status %d: %s", pv.Name, resp.StatusCode, clamped)
		p.finishTestEvent(start, pv, result, maskKey(servedKey(resp, pv)), clamped)
		return result
	}

	key := maskKey(servedKey(resp, pv))
	resp.Body = telemetry.WatchAnchored(resp.Body, start)
	content, err := bufferedTestContent(resp.Body, route.Dialect(), req.Model)
	if err != nil {
		result.Status = http.StatusOK
		result.Error = "failed to read upstream response: " + err.Error()
		p.finishTestEvent(start, pv, result, key, "")
		return result
	}

	watcher := resp.Body.(*telemetry.Watcher)
	result.Status = http.StatusOK
	result.TTFTMS = watcher.TTFTMS()
	result.Tokens = watcher.Tokens()
	result.Content = telemetry.ClampBody([]byte(content), telemetry.RespBodyCap)
	p.finishTestEvent(start, pv, result, key, "")

	return result
}

// testProvider resolves the configured provider for a test run. A name that is
// not in the config is 404; a disabled or keyless provider is 400.
func (p *Proxy) testProvider(name string) (*provider.Provider, int, error) {
	var found bool
	for _, pc := range p.Registry().ProviderConfigs() {
		if pc.Name != name {
			continue
		}
		found = true
		if pc.Disabled {
			return nil, http.StatusBadRequest, fmt.Errorf("provider %s is disabled", name)
		}
	}
	if !found {
		return nil, http.StatusNotFound, fmt.Errorf("provider %s not found", name)
	}

	pv, ok := p.Registry().Provider(name)
	if !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("provider %s is not active", name)
	}
	if pv.Keys.AliveCount() == 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("provider %s has no usable keys", name)
	}

	return pv, 0, nil
}

// testBody builds the canonical chat body for a test run. Effort is passed as
// reasoning_effort — the canonical input every translator already folds.
func testBody(req TestRequest) map[string]any {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = testDefaultMaxTokens
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   []any{map[string]any{"role": "user", "content": req.Prompt}},
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if req.Effort != "" {
		body["reasoning_effort"] = req.Effort
	}

	return body
}

// finishTestFailure records a run that never got a usable upstream response:
// deadline exceeded, client cancellation, or every key exhausted. Timeout wins
// over "cancelled" because tryKeys reports both the same way (served=true).
func (p *Proxy) finishTestFailure(start time.Time, ctx context.Context, pv *provider.Provider, result *TestResult, status int, errBody []byte, served bool, timeout time.Duration) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Error = fmt.Sprintf("timed out after %s", timeout)
	case served:
		result.Error = "request cancelled"
	default:
		clamped := telemetry.ClampBody(errBody, telemetry.RespBodyCap)
		result.Status = status
		result.Error = fmt.Sprintf("all keys exhausted for %s (status=%d): %s", pv.Name, status, clamped)
		p.finishTestEvent(start, pv, result, "", clamped)
		return
	}

	p.finishTestEvent(start, pv, result, "", "")
}

// bufferedTestContent drains the upstream SSE stream into the assistant text of
// the assembled chat.completion response, per wire dialect.
func bufferedTestContent(body io.Reader, dialect, modelName string) (string, error) {
	switch dialect {
	case "messages":
		data, err := adapter.BufferAnthropicToOpenAI(body, modelName)
		if err != nil {
			return "", err
		}
		return contentFromOpenAIJSON(data)
	case "google":
		data, err := adapter.BufferGoogleToOpenAI(body, modelName)
		if err != nil {
			return "", err
		}
		return contentFromOpenAIJSON(data)
	case "responses":
		data, err := adapter.BufferResponsesToOpenAI(body, modelName)
		if err != nil {
			return "", err
		}
		return contentFromOpenAIJSON(data)
	case "codex":
		data, err := adapter.BufferCodexToOpenAI(body, modelName)
		if err != nil {
			return "", err
		}
		return contentFromOpenAIJSON(data)
	}

	completion := bufferStream(body)
	if len(completion.Choices) == 0 {
		return "", nil
	}

	return completion.Choices[0].Message.Content, nil
}

// contentFromOpenAIJSON pulls the assistant text out of a buffered
// chat.completion document. Content is normally a string; array-of-parts
// shapes (each part carrying a text field) are flattened too.
func contentFromOpenAIJSON(data []byte) (string, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 || len(parsed.Choices[0].Message.Content) == 0 {
		return "", nil
	}

	raw := parsed.Choices[0].Message.Content
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil
	}
	var builder strings.Builder
	for _, part := range parts {
		if t, ok := part["text"].(string); ok {
			builder.WriteString(t)
		}
	}

	return builder.String(), nil
}

// finishTestEvent stamps the total duration on the result and records the run
// as one telemetry event. respBody carries the clamped upstream error body for
// failed runs; successful runs record no body.
func (p *Proxy) finishTestEvent(start time.Time, pv *provider.Provider, result *TestResult, key, respBody string) {
	result.DurationMS = time.Since(start).Milliseconds()

	p.recordTelemetry(telemetry.Event{
		Time:          start,
		Model:         result.UpstreamModel,
		Status:        result.Status,
		Provider:      pv.Name,
		UpstreamModel: result.UpstreamModel,
		Key:           key,
		TTFTMS:        result.TTFTMS,
		DurationMS:    result.DurationMS,
		TokensOut:     result.Tokens,
		Err:           result.Error,
		RespBody:      respBody,
	})
}
