package services

import (
	"context"

	"routerllm/internal/codex"
	"routerllm/internal/provider"
)

// codexAccessToken mints (or reuses) the Codex access token backing one account
// refresh token. Unlike cline, every codex provider refreshes against the same
// auth host, so one shared manager serves them all.
func (p *Proxy) codexAccessToken(ctx context.Context, refreshToken string, force bool) (string, error) {
	p.codexMu.Lock()
	manager := p.codexManager
	if manager == nil {
		store, err := codex.LoadAccountStore(codex.DefaultAccountsPath())
		if err != nil {
			p.log.Printf("codex accounts unavailable: %v", err)
			store = nil
		}
		manager = codex.NewManager(&codex.Client{HTTPClient: p.client}, store)
		p.codexManager = manager
	}
	p.codexMu.Unlock()

	return manager.AccessToken(ctx, refreshToken, force)
}

// pruneCodexManager drops the shared codex manager once no codex provider is
// configured, so its account store and token cache do not outlive the config
// that referenced them.
func (p *Proxy) pruneCodexManager(reg *provider.Registry) {
	for _, pc := range reg.ProviderConfigs() {
		if pc.Style == "codex" && !pc.Disabled {
			return
		}
	}

	p.codexMu.Lock()
	p.codexManager = nil
	p.codexMu.Unlock()
}
