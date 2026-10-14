package services

import (
	"context"

	"routerllm/internal/cline"
	"routerllm/internal/provider"
)

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
