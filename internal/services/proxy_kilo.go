package services

import (
	"routerllm/internal/kilo"
)

// kiloOrg resolves the per-account organization id for a gateway key. Kilo
// tokens are long-lived and never rotate, so a direct store lookup is stable
// across requests; no sticky cache is needed.
func (p *Proxy) kiloOrg(token string) string {
	store, err := kilo.LoadAccountStore(kilo.DefaultAccountsPath())
	if err != nil {
		p.log.Printf("kilo accounts unavailable: %v", err)
		return ""
	}

	return store.OrganizationID(token)
}
