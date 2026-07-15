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
	Defaults  RequestDefaults
}

type RequestDefaults struct {
	ReasoningEffort string
	EnableThinking  *bool
	ThinkingBudget  int
}

type Registry struct {
	routes map[string][]Route
	models []string
}

type routeSpec struct {
	provider string
	model    string
	defaults RequestDefaults
}

type routingRule struct {
	model  string
	routes []routeSpec
}

var alibabaModels = []string{
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
	qwenThinking := true
	rs := func(providerName, modelName string) routeSpec {
		return routeSpec{provider: providerName, model: modelName}
	}
	rd := func(providerName, modelName string, defaults RequestDefaults) routeSpec {
		return routeSpec{provider: providerName, model: modelName, defaults: defaults}
	}
	rules := []routingRule{
		{"claude-opus-4-8", []routeSpec{
			rd("freemodel-cc", "claude-opus-4-8", RequestDefaults{ThinkingBudget: 32000}),
			rs("aerolink", "claude-opus-4-8"),
		}},
		{"claude-opus-4-7", []routeSpec{
			rs("freemodel-cc", "claude-opus-4-7"),
			rs("aerolink", "claude-opus-4-7"),
		}},
		{"claude-opus-4-6", []routeSpec{
			rs("aerolink", "claude-opus-4-6"),
		}},
		{"gpt-5.5", []routeSpec{
			rd("freemodel-api", "gpt-5.5", RequestDefaults{ReasoningEffort: "high"}),
			rs("forge", "gpt-5.5"),
		}},
		{"glm-5.2", []routeSpec{
			rd("agentrouter", "glm-5.2", RequestDefaults{ReasoningEffort: "high"}),
			rd("forge", "glm-5.2", RequestDefaults{ReasoningEffort: "high"}),
		}},
		{"deepseek-v4-flash-free", []routeSpec{rs("opencode", "deepseek-v4-flash-free")}},
		{"mimo-v2.5-free", []routeSpec{rs("opencode", "mimo-v2.5-free")}},
		{"hy3-free", []routeSpec{
			rs("opencode", "hy3-free"),
			rs("forge", "tencent/hy3"),
		}},
		{"nemotron-3-ultra-free", []routeSpec{rs("opencode", "nemotron-3-ultra-free")}},
		{"north-mini-code-free", []routeSpec{rs("opencode", "north-mini-code-free")}},
		{"gpt-5.4", []routeSpec{rs("freemodel-api", "gpt-5.4")}},
		{"gpt-5.4-mini", []routeSpec{rs("freemodel-api", "gpt-5.4-mini")}},
		{"gpt-5.3-codex", []routeSpec{rs("freemodel-api", "gpt-5.3-codex")}},
		{"claude-fable-5", []routeSpec{
			rs("freemodel-cc", "claude-fable-5"),
			rs("aerolink", "claude-fable-5"),
		}},
		{"claude-sonnet-5", []routeSpec{
			rd("freemodel-cc", "claude-sonnet-5", RequestDefaults{ThinkingBudget: 1024}),
			rs("aerolink", "claude-sonnet-5"),
			rs("forge", "claude-sonnet-5"),
		}},
		{"claude-sonnet-4-6", []routeSpec{
			rd("freemodel-cc", "claude-sonnet-4-6", RequestDefaults{ThinkingBudget: 4096}),
			rs("aerolink", "claude-sonnet-4-6"),
			rs("forge", "claude-sonnet-4-6"),
		}},
		{"claude-haiku-4-5", []routeSpec{
			rd("freemodel-cc", "claude-haiku-4-5", RequestDefaults{ThinkingBudget: 4096}),
			rs("aerolink", "claude-haiku-4-5-20251001"),
			rs("forge", "claude-haiku-4-5-20251001"),
		}},
		{"kimi-k2.7-code", []routeSpec{
			rs("alibaba", "kimi-k2.7-code"),
			rd("forge", "kimi-k2.7-code", RequestDefaults{ReasoningEffort: "high"}),
		}},
		{"deepseek-r1", []routeSpec{rd("forge", "deepseek-r1", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v4-pro", []routeSpec{rd("forge", "deepseek-v4-pro", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v3.2", []routeSpec{rd("forge", "deepseek-v3.2", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v3.1", []routeSpec{rs("forge", "deepseek-v3.1")}},
		{"deepseek-v3", []routeSpec{rs("forge", "deepseek-v3")}},
		{"kimi-k2.6", []routeSpec{rs("forge", "kimi-k2.6")}},
		{"kimi-k2.5", []routeSpec{rs("forge", "kimi-k2.5")}},
		{"gemini-3.5-flash", []routeSpec{rs("forge", "gemini-3.5-flash")}},
		{"mimo-v2.5", []routeSpec{rd("forge", "mimo-v2.5", RequestDefaults{ReasoningEffort: "high"})}},
		{"mimo-v2.5-pro", []routeSpec{rd("forge", "mimo-v2.5-pro", RequestDefaults{ReasoningEffort: "high"})}},
		{"MiniMax-M3", []routeSpec{rd("forge", "MiniMax-M3", RequestDefaults{ReasoningEffort: "high"})}},
		{"MiniMax-M2.5", []routeSpec{rd("forge", "MiniMax-M2.5", RequestDefaults{ReasoningEffort: "high"})}},
	}
	for _, m := range alibabaModels {
		defaults := RequestDefaults{}
		if len(m) >= 4 && m[:4] == "qwen" {
			defaults.EnableThinking = &qwenThinking
		}
		rules = append(rules, routingRule{m, []routeSpec{rd("alibaba", m, defaults)}})
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
				rts = append(rts, Route{Provider: p, ModelName: spec.model, Defaults: spec.defaults})
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
