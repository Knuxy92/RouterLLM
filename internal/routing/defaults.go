package routing

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

func DefaultRules() []Rule {
	qwenThinking := true
	rs := func(providerName, modelName string) Spec {
		return Spec{Provider: providerName, Model: modelName}
	}
	rd := func(providerName, modelName string, defaults RequestDefaults) Spec {
		return Spec{Provider: providerName, Model: modelName, Defaults: defaults}
	}
	rules := []Rule{
		{"claude-opus-4-8", []Spec{
			rd("freemodel-cc", "claude-opus-4-8", RequestDefaults{ThinkingBudget: 32000}),
			rs("aerolink", "claude-opus-4-8"),
		}},
		{"claude-opus-4-7", []Spec{
			rs("freemodel-cc", "claude-opus-4-7"),
			rs("aerolink", "claude-opus-4-7"),
		}},
		{"claude-opus-4-6", []Spec{
			rs("aerolink", "claude-opus-4-6"),
		}},
		{"gpt-5.5", []Spec{
			rd("freemodel-api", "gpt-5.5", RequestDefaults{ReasoningEffort: "high"}),
			rs("forge", "gpt-5.5"),
		}},
		{"glm-5.2", []Spec{
			rd("agentrouter", "glm-5.2", RequestDefaults{ReasoningEffort: "high"}),
			rd("forge", "glm-5.2", RequestDefaults{ReasoningEffort: "high"}),
		}},
		{"deepseek-v4-flash-free", []Spec{rd("opencode", "deepseek-v4-flash-free", RequestDefaults{ThinkingBudget: 8192})}},
		{"mimo-v2.5-free", []Spec{rs("opencode", "mimo-v2.5-free")}},
		{"hy3-free", []Spec{
			rs("opencode", "hy3-free"),
			rs("forge", "tencent/hy3"),
		}},
		{"nemotron-3-ultra-free", []Spec{rs("opencode", "nemotron-3-ultra-free")}},
		{"north-mini-code-free", []Spec{rs("opencode", "north-mini-code-free")}},
		{"gpt-5.4", []Spec{rs("freemodel-api", "gpt-5.4")}},
		{"gpt-5.4-mini", []Spec{rs("freemodel-api", "gpt-5.4-mini")}},
		{"gpt-5.3-codex", []Spec{rs("freemodel-api", "gpt-5.3-codex")}},
		{"claude-fable-5", []Spec{
			rs("freemodel-cc", "claude-fable-5"),
			rs("aerolink", "claude-fable-5"),
		}},
		{"claude-sonnet-5", []Spec{
			rd("freemodel-cc", "claude-sonnet-5", RequestDefaults{ThinkingBudget: 1024}),
			rs("aerolink", "claude-sonnet-5"),
			rs("forge", "claude-sonnet-5"),
		}},
		{"claude-sonnet-4-6", []Spec{
			rd("freemodel-cc", "claude-sonnet-4-6", RequestDefaults{ThinkingBudget: 4096}),
			rs("aerolink", "claude-sonnet-4-6"),
			rs("forge", "claude-sonnet-4-6"),
		}},
		{"claude-haiku-4-5", []Spec{
			rd("freemodel-cc", "claude-haiku-4-5", RequestDefaults{ThinkingBudget: 4096}),
			rs("aerolink", "claude-haiku-4-5-20251001"),
			rs("forge", "claude-haiku-4-5-20251001"),
		}},
		{"kimi-k2.7-code", []Spec{
			rs("alibaba", "kimi-k2.7-code"),
			rd("forge", "kimi-k2.7-code", RequestDefaults{ReasoningEffort: "high"}),
		}},
		{"deepseek-r1", []Spec{rd("forge", "deepseek-r1", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v4-pro", []Spec{rd("forge", "deepseek-v4-pro", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v3.2", []Spec{rd("forge", "deepseek-v3.2", RequestDefaults{ReasoningEffort: "high"})}},
		{"deepseek-v3.1", []Spec{rs("forge", "deepseek-v3.1")}},
		{"deepseek-v3", []Spec{rs("forge", "deepseek-v3")}},
		{"kimi-k2.6", []Spec{rs("forge", "kimi-k2.6")}},
		{"kimi-k2.5", []Spec{rs("forge", "kimi-k2.5")}},
		{"gemini-3.5-flash", []Spec{rs("forge", "gemini-3.5-flash")}},
		{"mimo-v2.5", []Spec{rd("forge", "mimo-v2.5", RequestDefaults{ReasoningEffort: "high"})}},
		{"mimo-v2.5-pro", []Spec{rd("forge", "mimo-v2.5-pro", RequestDefaults{ReasoningEffort: "high"})}},
		{"MiniMax-M3", []Spec{rd("forge", "MiniMax-M3", RequestDefaults{ReasoningEffort: "high"})}},
		{"MiniMax-M2.5", []Spec{rd("forge", "MiniMax-M2.5", RequestDefaults{ReasoningEffort: "high"})}},
	}
	for _, m := range alibabaModels {
		defaults := RequestDefaults{}
		if len(m) >= 4 && m[:4] == "qwen" {
			defaults.EnableThinking = &qwenThinking
		}
		rules = append(rules, Rule{Model: m, Routes: []Spec{rd("alibaba", m, defaults)}})
	}
	return rules
}
