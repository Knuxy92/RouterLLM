package provider

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/keys"
	"routerllm/internal/model"
)

type Provider struct {
	Name     string
	BaseURL  string
	Style    string
	Keys     *keys.Manager
	Headers  map[string]string
	AuthMode string
	Query    string
	stats    *Stats
}

type Stats struct {
	requests atomic.Uint64
	errors   atomic.Uint64
}

func (s *Stats) Requests() uint64 {
	return s.requests.Load()
}

func (s *Stats) Errors() uint64 {
	return s.errors.Load()
}

func (p *Provider) RecordRequest() {
	p.stats.requests.Add(1)
}

func (p *Provider) RecordError() {
	p.stats.errors.Add(1)
}

func (p *Provider) Stats() *Stats {
	return p.stats
}

type Route struct {
	Provider  *Provider
	ModelName string
	Defaults  model.RequestDefaults
}

type Registry struct {
	providers map[string]*Provider
	routes    map[string][]Route
	models    []string
	configs   []config.ProviderConfig
	rules     []model.Rule
	skipped   []string
}

func NewRegistry(configs []config.ProviderConfig, rules []model.Rule, cooldown time.Duration) *Registry {
	return newRegistry(configs, rules, cooldown, nil)
}

func Rebuild(configs []config.ProviderConfig, rules []model.Rule, cooldown time.Duration, previous *Registry) *Registry {
	return newRegistry(configs, rules, cooldown, previous)
}

func newRegistry(configs []config.ProviderConfig, rules []model.Rule, cooldown time.Duration, previous *Registry) *Registry {
	providers := make(map[string]*Provider)
	sharedMgrs := make(map[string]*keys.Manager)

	for _, pc := range configs {
		if pc.Disabled {
			continue
		}

		var km *keys.Manager
		if pc.ShareKeys != "" {
			if existing, ok := sharedMgrs[pc.ShareKeys]; ok {
				km = existing
			} else {
				km = keys.New(pc.Keys, cooldown)
				sharedMgrs[pc.ShareKeys] = km
			}
		} else {
			km = keys.New(pc.Keys, cooldown)
		}

		km.Restore(previous.cooldownState(pc.Name))

		providers[pc.Name] = &Provider{
			Name:     pc.Name,
			BaseURL:  pc.BaseURL,
			Style:    pc.Style,
			Keys:     km,
			Headers:  pc.Headers,
			AuthMode: pc.AuthMode,
			Query:    pc.Query,
			stats:    previous.statsFor(pc.Name),
		}
	}

	routes := make(map[string][]Route)
	modelSet := make(map[string]bool)
	var skipped []string

	for _, rule := range rules {
		var rts []Route
		for _, spec := range rule.Routes {
			if spec.Disabled {
				skipped = append(skipped, fmt.Sprintf("%s→%s (disabled)", rule.ModelID, spec.Provider))
				continue
			}

			p, ok := providers[spec.Provider]
			if !ok {
				skipped = append(skipped, fmt.Sprintf("%s→%s (provider unavailable)", rule.ModelID, spec.Provider))
				continue
			}

			rts = append(rts, Route{Provider: p, ModelName: spec.Model, Defaults: spec.Defaults})
		}
		if len(rts) > 0 {
			routes[rule.ModelID] = rts
			modelSet[rule.ModelID] = true
		}
	}

	var models []string
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)

	return &Registry{providers: providers, routes: routes, models: models, configs: configs, rules: rules, skipped: skipped}
}

func (r *Registry) cooldownState(name string) map[string]time.Time {
	if r == nil {
		return nil
	}

	p, ok := r.providers[name]
	if !ok {
		return nil
	}

	return p.Keys.Snapshot()
}

func (r *Registry) statsFor(name string) *Stats {
	if r == nil {
		return &Stats{}
	}

	if p, ok := r.providers[name]; ok {
		return p.stats
	}

	return &Stats{}
}

func (r *Registry) Routes(model string) []Route {
	return r.routes[model]
}

func (r *Registry) AllModels() []string {
	return r.models
}

func (r *Registry) ActiveProviders() int {
	return len(r.providers)
}

func (r *Registry) TotalProviders() int {
	return len(r.configs)
}

func (r *Registry) SkippedRoutes() []string {
	if r.skipped == nil {
		return []string{}
	}

	return r.skipped
}

func (r *Registry) ProviderConfigs() []config.ProviderConfig {
	return r.configs
}

func (r *Registry) Rules() []model.Rule {
	return r.rules
}

func (r *Registry) Provider(name string) (*Provider, bool) {
	p, ok := r.providers[name]

	return p, ok
}
