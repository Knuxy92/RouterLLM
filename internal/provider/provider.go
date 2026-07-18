package provider

import (
	"sort"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/keys"
	"routerllm/internal/routing"
)

type Provider struct {
	Name     string
	BaseURL  string
	Style    string
	Keys     *keys.Manager
	Headers  map[string]string
	AuthMode string
	Query    string
}

type Route struct {
	Provider  *Provider
	ModelName string
	Defaults  routing.RequestDefaults
}

type Registry struct {
	routes    map[string][]Route
	models    []string
	providers map[string]*Provider
}

func NewRegistry(configs []config.ProviderConfig, rules []routing.Rule, cooldown time.Duration) *Registry {
	providers := make(map[string]*Provider)
	sharedMgrs := make(map[string]*keys.Manager)

	for _, pc := range configs {
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
		providers[pc.Name] = &Provider{
			Name:     pc.Name,
			BaseURL:  pc.BaseURL,
			Style:    pc.Style,
			Keys:     km,
			Headers:  pc.Headers,
			AuthMode: pc.AuthMode,
			Query:    pc.Query,
		}
	}

	routes := make(map[string][]Route)
	modelSet := make(map[string]bool)

	for _, rule := range rules {
		var rts []Route
		for _, spec := range rule.Routes {
			if p, ok := providers[spec.Provider]; ok {
				rts = append(rts, Route{Provider: p, ModelName: spec.Model, Defaults: spec.Defaults})
			}
		}
		if len(rts) > 0 {
			routes[rule.Model] = rts
			modelSet[rule.Model] = true
		}
	}

	var models []string
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)

	return &Registry{routes: routes, models: models, providers: providers}
}

func (r *Registry) KeyManager(name string) *keys.Manager {
	if p, ok := r.providers[name]; ok {
		return p.Keys
	}
	return nil
}

func (r *Registry) ProviderNames() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Routes(model string) []Route {
	return r.routes[model]
}

func (r *Registry) AllModels() []string {
	return r.models
}
