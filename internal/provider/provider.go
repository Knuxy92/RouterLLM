package provider

import (
	"sort"
	"time"

	"agentrouter/internal/config"
	"agentrouter/internal/keys"
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
}

type Registry struct {
	routes map[string][]Route
	models []string
}

type routeSpec struct {
	provider string
	model    string
}

type routingRule struct {
	model  string
	routes []routeSpec
}

var alibabaModels = []string{
	"kimi-k2.7-code",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.7-max-preview",
	"qwen3.6-27b",
	"qwen3.6-max-preview",
	"qwen3.6-35b-a3b",
	"qwen3.6-flash",
	"qwen3.6-plus",
	"qwen3.5-flash",
	"qwen3.5-122b-a10b",
	"qwen3.5-35b-a3b",
	"qwen3.5-plus",
	"qwen3.5-omni-plus",
	"qwen3.5-omni-flash",
}

func routingRules() []routingRule {
	rules := []routingRule{
		{"claude-opus-4-8", []routeSpec{{"freemodel-cc", "claude-opus-4-8"}}},
		{"claude-opus-4-7", []routeSpec{{"freemodel-cc", "claude-opus-4-7"}}},
		{"claude-opus-4-6", []routeSpec{{"freemodel-cc", "claude-opus-4-6"}}},
		{"gpt-5.5", []routeSpec{{"freemodel-api", "gpt-5.5"}}},
		{"glm-5.2", []routeSpec{{"agentrouter", "glm-5.2"}}},
		{"deepseek-v4-flash-free", []routeSpec{{"opencode", "deepseek-v4-flash-free"}}},
		{"mimo-v2.5-free", []routeSpec{{"opencode", "mimo-v2.5-free"}}},
		{"hy3-free", []routeSpec{{"opencode", "hy3-free"}}},
		{"nemotron-3-ultra-free", []routeSpec{{"opencode", "nemotron-3-ultra-free"}}},
		{"north-mini-code-free", []routeSpec{{"opencode", "north-mini-code-free"}}},
		{"gpt-5.6", []routeSpec{{"freemodel-api", "gpt-5.6"}}},
		{"gpt-5.4", []routeSpec{{"freemodel-api", "gpt-5.4"}}},
		{"gpt-5.4-mini", []routeSpec{{"freemodel-api", "gpt-5.4-mini"}}},
		{"gpt-5.3-codex", []routeSpec{{"freemodel-api", "gpt-5.3-codex"}}},
		{"claude-fable-5", []routeSpec{{"freemodel-cc", "claude-fable-5"}}},
		{"claude-sonnet-5", []routeSpec{{"freemodel-cc", "claude-sonnet-5"}}},
		{"claude-sonnet-4-6", []routeSpec{{"freemodel-cc", "claude-sonnet-4-6"}}},
		{"claude-haiku-4-5", []routeSpec{{"freemodel-cc", "claude-haiku-4-5"}}},
	}
	for _, m := range alibabaModels {
		rules = append(rules, routingRule{m, []routeSpec{{"alibaba", m}}})
	}
	return rules
}

func NewRegistry(configs []config.ProviderConfig, cooldown time.Duration) *Registry {
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

	for _, rule := range routingRules() {
		var rts []Route
		for _, spec := range rule.routes {
			if p, ok := providers[spec.provider]; ok {
				rts = append(rts, Route{Provider: p, ModelName: spec.model})
			}
		}
		if len(rts) > 0 {
			routes[rule.model] = rts
			modelSet[rule.model] = true
		}
	}

	var models []string
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)

	return &Registry{routes: routes, models: models}
}

func (r *Registry) Routes(model string) []Route {
	return r.routes[model]
}

func (r *Registry) AllModels() []string {
	return r.models
}
