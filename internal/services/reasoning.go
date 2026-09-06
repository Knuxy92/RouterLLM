package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"routerllm/internal/model"
	"routerllm/internal/provider"
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

// reasoningDialectLabel names the reasoning dialect a provider's requests are
// emitted in — the reasoning_style for openai-style providers, the fixed
// style dialect otherwise.
func reasoningDialectLabel(pv *provider.Provider) string {
	if pv.Style != "openai" {
		return pv.Style
	}
	if pv.ReasoningStyle == "" {
		return "openai"
	}
	return pv.ReasoningStyle
}

// reasoningSummary renders the reasoning settings carried on an outbound
// request body — whatever dialect they ended up in — for the debug trace.
func reasoningSummary(body map[string]any) string {
	var parts []string
	if effort, ok := body["reasoning_effort"].(string); ok {
		parts = append(parts, "effort="+effort)
	}
	if budget := intValue(body["thinking_budget"]); budget > 0 {
		parts = append(parts, "budget="+strconv.Itoa(budget))
	}
	if v, ok := body["enable_thinking"].(bool); ok {
		parts = append(parts, fmt.Sprintf("enable_thinking=%v", v))
	}
	if r, ok := body["reasoning"].(map[string]any); ok {
		if data, err := json.Marshal(r); err == nil {
			parts = append(parts, "reasoning="+string(data))
		}
	}
	if t, ok := body["thinking"].(map[string]any); ok {
		if data, err := json.Marshal(t); err == nil {
			parts = append(parts, "thinking="+string(data))
		}
	}
	return strings.Join(parts, " ")
}
