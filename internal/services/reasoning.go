package services

import (
	"routerllm/internal/model"
)

// Reasoning settings arrive in several dialects — OpenAI's reasoning_effort
// string, OpenRouter's reasoning map, Qwen's enable_thinking/thinking_budget
// pair, and Anthropic's thinking block. Everything is folded into two
// canonical keys on the request body (reasoning_effort + thinking_budget,
// plus the internal reasoning_exclude marker) and re-emitted in whichever
// dialect the target provider speaks:
//
//	client reasoning map > client reasoning_effort / enable_thinking > route defaults
var reasoningEffortAliases = map[string]string{
	"none":    "none",
	"minimal": "minimal",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "xhigh",
	"max":     "max",
	"ultra":   "max",
}

func normalizeReasoningEffort(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return reasoningEffortAliases[s]
}

func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// applyCanonicalDefaults folds client reasoning dialects plus route defaults
// into the canonical keys (reasoning_effort/thinking_budget) on the body.
func applyCanonicalDefaults(body map[string]any, defaults model.RequestDefaults) {
	canonicalizeReasoning(body, defaults)
}

// canonicalizeReasoning folds every reasoning dialect on the body plus the
// route defaults into canonical keys, deleting the dialect keys. It returns a
// notice when an effort value was dropped as unknown.
func canonicalizeReasoning(body map[string]any, defaults model.RequestDefaults) string {
	effort := ""
	budget := 0
	exclude := false
	hasExclude := false
	notice := ""

	if r, ok := body["reasoning"].(map[string]any); ok {
		effort = normalizeReasoningEffort(r["effort"])
		budget = intValue(r["max_tokens"])
		if enabled, ok := r["enabled"].(bool); ok && !enabled {
			effort = "none"
		}
		if e, ok := r["exclude"].(bool); ok {
			exclude, hasExclude = e, true
		}
	}
	if effort == "" {
		if e := normalizeReasoningEffort(body["reasoning_effort"]); e != "" {
			effort = e
		}
	}
	if enabled, ok := body["enable_thinking"].(bool); ok && !enabled {
		effort = "none"
	}

	if effort == "" {
		if e := normalizeReasoningEffort(defaults.ReasoningEffort); e != "" {
			effort = e
		} else if defaults.ReasoningEffort != "" {
			notice = "unknown reasoning_effort " + defaults.ReasoningEffort
		}
		if defaults.EnableThinking != nil && !*defaults.EnableThinking {
			effort = "none"
		}
	}
	if budget == 0 {
		budget = defaults.ThinkingBudget
	}

	delete(body, "reasoning")
	delete(body, "reasoning_effort")
	delete(body, "enable_thinking")
	delete(body, "thinking")
	delete(body, "include_reasoning")
	delete(body, "thinking_budget")

	if effort != "" {
		body["reasoning_effort"] = effort
	}
	if budget > 0 {
		body["thinking_budget"] = budget
	}
	if hasExclude {
		body["reasoning_exclude"] = exclude
	}
	return notice
}

// applyReasoningDialect rewrites the canonical keys into the outbound dialect
// of an openai-style provider (openai | openrouter | qwen) in place.
func applyReasoningDialect(body map[string]any, style string) {
	effort, _ := body["reasoning_effort"].(string)
	budget := intValue(body["thinking_budget"])
	exclude, hasExclude := body["reasoning_exclude"].(bool)

	delete(body, "reasoning_effort")
	delete(body, "thinking_budget")
	delete(body, "reasoning_exclude")

	switch style {
	case "qwen":
		if effort != "" {
			body["enable_thinking"] = effort != "none"
		}
		if budget > 0 {
			body["thinking_budget"] = budget
		}
	case "openrouter":
		if effort == "" && budget == 0 && !hasExclude {
			return
		}
		reasoning := map[string]any{}
		if effort != "" && effort != "none" {
			reasoning["effort"] = effort
		}
		if effort == "none" {
			reasoning["enabled"] = false
		}
		if budget > 0 {
			reasoning["max_tokens"] = budget
		}
		if hasExclude {
			reasoning["exclude"] = exclude
		}
		body["reasoning"] = reasoning
	default:
		if effort != "" {
			body["reasoning_effort"] = effort
		}
	}
}
