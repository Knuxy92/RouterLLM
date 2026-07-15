package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"agentrouter/internal/adapter"
	"agentrouter/internal/dto"
	"agentrouter/internal/keys"
	"agentrouter/internal/provider"
	"agentrouter/internal/util"

	"github.com/gin-gonic/gin"
)

const maxRetries = 3

var deadStatuses = map[int]bool{
	401: true,
	402: true,
	403: true,
}

var transientStatuses = map[int]bool{
	408: true,
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

type Proxy struct {
	registry *provider.Registry
	client   *http.Client
	log      *log.Logger
}

func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger) *Proxy {
	return &Proxy{registry: reg, client: client, log: log}
}

func (p *Proxy) Forward(path string, c *gin.Context) {
	raw, err := c.GetRawData()
	if err != nil {
		c.String(http.StatusBadRequest, "failed to read request body: "+err.Error())
		return
	}
	body, clientStream, err := dto.ParseAndForceStream(raw)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	model, _ := body["model"].(string)
	routes := p.registry.Routes(model)
	if len(routes) == 0 {
		c.String(http.StatusNotFound, fmt.Sprintf("model %q not found", model))
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
			c.String(http.StatusNotFound, fmt.Sprintf("model %q not available for %s", model, path))
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
			c.String(http.StatusInternalServerError, "failed to encode body: "+err.Error())
			return
		}

		resp, status, errBody, served := p.tryKeys(pv, reqPath, reqBody, c)
		if served {
			return
		}

		if resp == nil {
			p.log.Printf("all keys exhausted for %s via %s, falling back...", model, pv.Name)
			lastStatus = status
			lastErrBody = errBody
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			p.log.Printf("upstream %s returned HTTP %d, passing through", pv.Name, resp.StatusCode)
			c.Header("Access-Control-Allow-Origin", "*")
			c.Data(resp.StatusCode, "application/json", eb)
			return
		}

		p.log.Printf("serving %s via %s (HTTP %d)", path, pv.Name, resp.StatusCode)

		if pv.Style == "anthropic" {
			p.serveAnthropic(resp, clientStream, route.ModelName, c)
		} else {
			p.serveOpenAI(resp, clientStream, c)
		}
		return
	}

	if lastErrBody != nil {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Data(lastStatus, "application/json", lastErrBody)
	} else {
		c.String(http.StatusBadGateway, "all providers exhausted for model "+model)
	}
}

func cloneBody(body map[string]any) map[string]any {
	clone := make(map[string]any, len(body))
	for k, v := range body {
		clone[k] = v
	}
	return clone
}

func applyDefaults(body map[string]any, defaults provider.RequestDefaults) {
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

func (p *Proxy) tryKeys(pv *provider.Provider, path string, reqBody []byte, c *gin.Context) (*http.Response, int, []byte, bool) {
	maxAttempts := pv.Keys.Count()
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
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, pv.BaseURL+path+pv.Query, bytes.NewReader(reqBody))
			if err != nil {
				c.String(http.StatusInternalServerError, "failed to build request: "+err.Error())
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

			r, err := p.client.Do(req)
			if err != nil {
				p.log.Printf("proxy error via %s (retry %d/%d): %v", pv.Name, retry+1, maxRetries, err)
				lastStatus = http.StatusBadGateway
				lastErrBody = []byte(err.Error())
				if retry < maxRetries-1 {
					if p.backoff(c.Request.Context(), retry) {
						return nil, lastStatus, lastErrBody, true
					}
					continue
				}
				break
			}

			if deadStatuses[r.StatusCode] {
				eb, _ := io.ReadAll(r.Body)
				r.Body.Close()
				p.log.Printf("key %s dead (HTTP %d) via %s, switching...", keys.Mask(key), r.StatusCode, pv.Name)
				lastStatus = r.StatusCode
				lastErrBody = eb
				pv.Keys.MarkDead(key)
				break
			}

			if transientStatuses[r.StatusCode] {
				eb, _ := io.ReadAll(r.Body)
				r.Body.Close()
				p.log.Printf("upstream %s transient (HTTP %d), retry %d/%d", pv.Name, r.StatusCode, retry+1, maxRetries)
				lastStatus = r.StatusCode
				lastErrBody = eb
				if retry < maxRetries-1 {
					if p.backoff(c.Request.Context(), retry) {
						return nil, lastStatus, lastErrBody, true
					}
					continue
				}
				break
			}

			return r, 0, nil, false
		}
	}

	return nil, lastStatus, lastErrBody, false
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

func (p *Proxy) serveOpenAI(resp *http.Response, clientStream bool, c *gin.Context) {
	if clientStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
		c.Status(http.StatusOK)
		if err := util.StreamSSE(resp.Body, c.Writer); err != nil {
			p.log.Printf("stream error: %v", err)
		}
		return
	}

	result := bufferStream(resp.Body)
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(resp.StatusCode, result)
}

func (p *Proxy) serveAnthropic(resp *http.Response, clientStream bool, modelName string, c *gin.Context) {
	if clientStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
		c.Status(http.StatusOK)
		if err := adapter.StreamAnthropicToOpenAI(resp.Body, c.Writer, modelName); err != nil {
			p.log.Printf("anthropic stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferAnthropicToOpenAI(resp.Body, modelName)
	if err != nil {
		c.String(http.StatusBadGateway, "failed to translate response: "+err.Error())
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Data(http.StatusOK, "application/json", openaiBody)
}
