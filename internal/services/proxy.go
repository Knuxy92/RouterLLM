package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"agentrouter/internal/adapter"
	"agentrouter/internal/dto"
	"agentrouter/internal/keys"
	"agentrouter/internal/provider"
	"agentrouter/internal/util"

	"github.com/gin-gonic/gin"
)

var switchableStatuses = map[int]bool{
	401: true,
	402: true,
	403: true,
	429: true,
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

		var reqBody []byte
		var reqPath string
		var authHeader string

		if pv.Style == "anthropic" {
			reqBody, reqPath, err = adapter.TranslateRequest(body, route.ModelName)
			authHeader = "x-api-key"
		} else {
			body["model"] = route.ModelName
			reqBody, err = json.Marshal(body)
			reqPath = path
			authHeader = "Authorization"
		}
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to encode body: "+err.Error())
			return
		}

		resp, status, errBody, served := p.tryKeys(pv, reqPath, reqBody, authHeader, c)
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

		if resp.StatusCode >= 500 {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			p.log.Printf("upstream %s returned HTTP %d, falling back...", pv.Name, resp.StatusCode)
			lastStatus = resp.StatusCode
			lastErrBody = eb
			continue
		}

		if resp.StatusCode != http.StatusOK {
			eb, _ := io.ReadAll(resp.Body)
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

func (p *Proxy) tryKeys(pv *provider.Provider, path string, reqBody []byte, authHeader string, c *gin.Context) (*http.Response, int, []byte, bool) {
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

		var authValue string
		if authHeader == "Authorization" {
			authValue = "Bearer " + key
		} else {
			authValue = key
		}

		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, pv.BaseURL+path, bytes.NewReader(reqBody))
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to build request: "+err.Error())
			return nil, 0, nil, true
		}
		for k, v := range pv.Headers {
			req.Header.Set(k, v)
		}
		req.Header.Set(authHeader, authValue)

		r, err := p.client.Do(req)
		if err != nil {
			p.log.Printf("proxy error via %s: %v", pv.Name, err)
			c.String(http.StatusBadGateway, err.Error())
			return nil, 0, nil, true
		}

		if switchableStatuses[r.StatusCode] {
			eb, _ := io.ReadAll(r.Body)
			r.Body.Close()
			p.log.Printf("key %s dead (HTTP %d) via %s, switching...", keys.Mask(key), r.StatusCode, pv.Name)
			lastStatus = r.StatusCode
			lastErrBody = eb
			pv.Keys.MarkDead(key)
			continue
		}

		return r, 0, nil, false
	}

	return nil, lastStatus, lastErrBody, false
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

	anthropicBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.String(http.StatusBadGateway, "failed to read upstream response: "+err.Error())
		return
	}

	openaiBody, err := adapter.TranslateResponse(anthropicBody)
	if err != nil {
		c.String(http.StatusBadGateway, "failed to translate response: "+err.Error())
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Data(http.StatusOK, "application/json", openaiBody)
}
