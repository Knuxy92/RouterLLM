package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"routerllm/internal/cline"
	"routerllm/internal/codex"
	"routerllm/internal/provider"
	"routerllm/internal/settings"
	"routerllm/internal/workbuddy"
)

type ModelCatalog struct {
	Provider string   `json:"provider"`
	Style    string   `json:"style"`
	Source   string   `json:"source"`
	Models   []string `json:"models"`
}

func (p *Proxy) ListProviderModels(ctx context.Context, name string) (ModelCatalog, error) {
	pv, ok := p.Registry().Provider(name)
	if !ok {
		return ModelCatalog{}, fmt.Errorf("provider %q is not configured or disabled", name)
	}

	key := pv.Keys.LiveKey()
	if pv.AuthMode != "none" && key == "" {
		return ModelCatalog{}, fmt.Errorf("provider %q has no available keys", name)
	}

	cfg := settings.Current().Admin.Models
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var source string
	var models []string
	var err error

	switch pv.Style {
	case "codex":
		source, models, err = p.codexModels(ctx, pv, key)
	case "workbuddy":
		source, models, err = p.workbuddyModels(ctx, pv, key, timeout)
	case "cline":
		source, models, err = p.clineModels(ctx, pv, key)
	default:
		source, models, err = p.fallbackModels(ctx, pv, key)
	}
	if err != nil {
		return ModelCatalog{}, err
	}

	return ModelCatalog{Provider: name, Style: pv.Style, Source: source, Models: models}, nil
}

func (p *Proxy) codexModels(ctx context.Context, pv *provider.Provider, key string) (string, []string, error) {
	token, err := p.codexAccessToken(ctx, key, false)
	if err != nil {
		return "", nil, err
	}

	req, err := newCatalogRequest(ctx, pv, pv.BaseURL+"/codex/models?client_version=26.915.31029")
	if err != nil {
		return "", nil, err
	}
	codex.SetHeaders(req.Header, token)

	body, err := p.doCatalogRequest(req, pv.Name)
	if err != nil {
		return "", nil, err
	}

	models, err := codex.ParseModels(body)
	if err != nil {
		return "", nil, err
	}

	return "catalog", models, nil
}

func (p *Proxy) clineModels(ctx context.Context, pv *provider.Provider, key string) (string, []string, error) {
	token, err := p.clineAccessToken(ctx, pv.BaseURL, key, false)
	if err != nil {
		return "", nil, err
	}

	req, err := newCatalogRequest(ctx, pv, pv.BaseURL+"/v1/ai/cline/recommended-models")
	if err != nil {
		return "", nil, err
	}
	cline.SetHeaders(req.Header, token, "catalog")
	req.Header.Set("X-CLIENT-TYPE", "cline-sdk")

	body, err := p.doCatalogRequest(req, pv.Name)
	if err != nil {
		return "", nil, err
	}

	models, err := cline.ParseModels(body)
	if err != nil {
		return "", nil, err
	}

	return "catalog", models, nil
}

func (p *Proxy) workbuddyModels(ctx context.Context, pv *provider.Provider, key string, timeout time.Duration) (string, []string, error) {
	realm, uid, _ := p.workbuddyAccount(key)
	token, err := p.workbuddyAccessToken(ctx, realm, uid, key, false)
	if err != nil {
		return "", nil, err
	}

	models, err := workbuddy.CatalogClient(timeout).FetchModels(ctx, realm, uid, token)
	if err != nil {
		return "", nil, err
	}

	return "catalog", models, nil
}

func (p *Proxy) fallbackModels(ctx context.Context, pv *provider.Provider, key string) (string, []string, error) {
	req, err := newCatalogRequest(ctx, pv, pv.BaseURL+"/v1/models")
	if err != nil {
		return "", nil, err
	}
	if pv.AuthMode != "none" {
		applyAuthHeaders(req.Header, key, pv.AuthMode)
	}

	body, err := p.doCatalogRequest(req, pv.Name)
	if err != nil {
		return "", nil, err
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, err
	}

	seen := make(map[string]bool, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if entry.ID == "" || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		models = append(models, entry.ID)
	}

	return "fallback", models, nil
}

func newCatalogRequest(ctx context.Context, pv *provider.Provider, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create model catalog request: %w", err)
	}
	for header, value := range pv.Headers {
		req.Header.Set(header, value)
	}

	return req, nil
}

func (p *Proxy) doCatalogRequest(req *http.Request, providerName string) ([]byte, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request model catalog: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(settings.Current().Admin.Models.BodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("read model catalog response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		p.log.Printf("model catalog: provider=%s status=%d body=%s", providerName, resp.StatusCode, briefBody(body))
		return nil, fmt.Errorf("upstream model catalog request failed with status %d", resp.StatusCode)
	}

	return body, nil
}
