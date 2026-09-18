package services

import (
	"encoding/json"
	"strings"
	"testing"

	"routerllm/internal/model"
)

func testBool(b bool) *bool {
	return &b
}

func testJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(data)
}

func TestCanonicalizeReasoningPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]any
		defaults   model.RequestDefaults
		wantBody   map[string]any
		wantNotice string
	}{
		{
			name: "client reasoning map beats reasoning_effort and defaults",
			body: map[string]any{
				"model":            "m",
				"reasoning":        map[string]any{"effort": "low"},
				"reasoning_effort": "high",
			},
			defaults: model.RequestDefaults{ReasoningEffort: "max"},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
			},
		},
		{
			name: "client reasoning_effort beats defaults",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
			},
			defaults: model.RequestDefaults{ReasoningEffort: "max"},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
			},
		},
		{
			name: "defaults apply when client sent nothing",
			body: map[string]any{"model": "m"},
			defaults: model.RequestDefaults{
				ReasoningEffort: "max",
				ThinkingBudget:  4096,
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "max",
				"thinking_budget":  4096,
			},
		},
		{
			name:       "empty reasoning map falls through to defaults",
			body:       map[string]any{"model": "m", "reasoning": map[string]any{}},
			defaults:   model.RequestDefaults{ReasoningEffort: "high"},
			wantBody:   map[string]any{"model": "m", "reasoning_effort": "high"},
			wantNotice: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
			if !strings.Contains(notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want substring %q", notice, tt.wantNotice)
			}
		})
	}
}

func TestCanonicalizeReasoningMapFold(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]any
		defaults   model.RequestDefaults
		wantBody   map[string]any
		wantNotice string
	}{
		{
			name: "effort and max_tokens fold to canonical keys",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "high", "max_tokens": float64(2048)},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
				"thinking_budget":  2048,
			},
		},
		{
			name: "int max_tokens also folds",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "low", "max_tokens": 512},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
				"thinking_budget":  512,
			},
		},
		{
			name: "enabled false forces none over effort in the same map",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "high", "enabled": false},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "exclude is carried to reasoning_exclude",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "medium", "exclude": true},
			},
			wantBody: map[string]any{
				"model":             "m",
				"reasoning_effort":  "medium",
				"reasoning_exclude": true,
			},
		},
		{
			name: "map max_tokens beats defaults budget",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "low", "max_tokens": 128},
			},
			defaults: model.RequestDefaults{ThinkingBudget: 4096},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
				"thinking_budget":  128,
			},
		},
		{
			name:     "effort only, no budget, no thinking_budget key",
			body:     map[string]any{"model": "m", "reasoning": map[string]any{"effort": "xhigh"}},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "xhigh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
			if !strings.Contains(notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want substring %q", notice, tt.wantNotice)
			}
		})
	}
}

func TestCanonicalizeReasoningEnableThinking(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]any
		defaults model.RequestDefaults
		wantBody map[string]any
	}{
		{
			name: "client enable_thinking false forces none over defaults high",
			body: map[string]any{
				"model":           "m",
				"enable_thinking": false,
			},
			defaults: model.RequestDefaults{ReasoningEffort: "high"},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "enable_thinking false overrides client reasoning_effort",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
				"enable_thinking":  false,
			},
			defaults: model.RequestDefaults{ReasoningEffort: "max"},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "enable_thinking false overrides reasoning map",
			body: map[string]any{
				"model":           "m",
				"reasoning":       map[string]any{"effort": "high"},
				"enable_thinking": false,
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "enable_thinking true does not force none",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
				"enable_thinking":  true,
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
		})
	}
}

func TestCanonicalizeReasoningThinkingDialect(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]any
		defaults model.RequestDefaults
		wantBody map[string]any
	}{
		{
			name: "thinking budget_tokens folds to thinking_budget",
			body: map[string]any{
				"model":    "m",
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 5000},
			},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 5000,
			},
		},
		{
			name: "thinking budget_tokens beats route default budget",
			body: map[string]any{
				"model":    "m",
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 5000},
			},
			defaults: model.RequestDefaults{ThinkingBudget: 4096},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 5000,
			},
		},
		{
			name: "thinking disabled forces none",
			body: map[string]any{
				"model":    "m",
				"thinking": map[string]any{"type": "disabled"},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "thinking disabled beats route default effort",
			body: map[string]any{
				"model":    "m",
				"thinking": map[string]any{"type": "disabled"},
			},
			defaults: model.RequestDefaults{ReasoningEffort: "high"},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
		},
		{
			name: "thinking disabled does not override explicit reasoning_effort",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
				"thinking":         map[string]any{"type": "disabled"},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
			},
		},
		{
			name: "thinking disabled does not override reasoning map effort",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"effort": "low"},
				"thinking":  map[string]any{"type": "disabled"},
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
			},
		},
		{
			name: "reasoning max_tokens beats thinking budget_tokens",
			body: map[string]any{
				"model":     "m",
				"reasoning": map[string]any{"max_tokens": 2048},
				"thinking":  map[string]any{"type": "enabled", "budget_tokens": 5000},
			},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 2048,
			},
		},
		{
			name: "top-level thinking_budget beats route default",
			body: map[string]any{
				"model":           "m",
				"thinking_budget": 999,
			},
			defaults: model.RequestDefaults{ThinkingBudget: 4096},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 999,
			},
		},
		{
			name: "thinking budget_tokens beats top-level thinking_budget",
			body: map[string]any{
				"model":           "m",
				"thinking":        map[string]any{"type": "enabled", "budget_tokens": 5000},
				"thinking_budget": 999,
			},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 5000,
			},
		},
		{
			name: "thinking enabled without budget adds nothing",
			body: map[string]any{
				"model":    "m",
				"thinking": map[string]any{"type": "enabled"},
			},
			wantBody: map[string]any{
				"model": "m",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
		})
	}
}

func TestCanonicalizeReasoningDefaults(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]any
		defaults   model.RequestDefaults
		wantBody   map[string]any
		wantNotice string
	}{
		{
			name:     "defaults effort applied on empty body",
			body:     map[string]any{"model": "m"},
			defaults: model.RequestDefaults{ReasoningEffort: "max"},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "max"},
		},
		{
			name:     "enable_thinking false default forces none",
			body:     map[string]any{"model": "m"},
			defaults: model.RequestDefaults{ReasoningEffort: "high", EnableThinking: testBool(false)},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "none"},
		},
		{
			name:     "enable_thinking true default leaves effort",
			body:     map[string]any{"model": "m"},
			defaults: model.RequestDefaults{ReasoningEffort: "low", EnableThinking: testBool(true)},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "low"},
		},
		{
			name:     "thinking budget default fills thinking_budget",
			body:     map[string]any{"model": "m"},
			defaults: model.RequestDefaults{ThinkingBudget: 1024},
			wantBody: map[string]any{"model": "m", "thinking_budget": 1024},
		},
		{
			name:     "no defaults, no client keys injects nothing",
			body:     map[string]any{"model": "m"},
			wantBody: map[string]any{"model": "m"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
			if !strings.Contains(notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want substring %q", notice, tt.wantNotice)
			}
		})
	}
}

func TestCanonicalizeReasoningAliasesAndUnknown(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]any
		defaults   model.RequestDefaults
		wantBody   map[string]any
		wantNotice string
	}{
		{
			name:     "ultra passes through from client",
			body:     map[string]any{"model": "m", "reasoning_effort": "ultra"},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "ultra"},
		},
		{
			name:     "ultra passes through from defaults",
			body:     map[string]any{"model": "m"},
			defaults: model.RequestDefaults{ReasoningEffort: "ultra"},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "ultra"},
		},
		{
			name:       "unknown defaults effort injects nothing and returns notice",
			body:       map[string]any{"model": "m"},
			defaults:   model.RequestDefaults{ReasoningEffort: "mega"},
			wantBody:   map[string]any{"model": "m"},
			wantNotice: "unknown reasoning_effort",
		},
		{
			name:       "unknown defaults effort with budget still fills budget",
			body:       map[string]any{"model": "m"},
			defaults:   model.RequestDefaults{ReasoningEffort: "mega", ThinkingBudget: 256},
			wantBody:   map[string]any{"model": "m", "thinking_budget": 256},
			wantNotice: "unknown reasoning_effort mega",
		},
		{
			name:     "unknown client effort falls through to defaults",
			body:     map[string]any{"model": "m", "reasoning_effort": "bogus"},
			defaults: model.RequestDefaults{ReasoningEffort: "high"},
			wantBody: map[string]any{"model": "m", "reasoning_effort": "high"},
		},
		{
			name:     "unknown client effort with no defaults injects nothing",
			body:     map[string]any{"model": "m", "reasoning_effort": "bogus"},
			wantBody: map[string]any{"model": "m"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := canonicalizeReasoning(tt.body, tt.defaults)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
			if !strings.Contains(notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want substring %q", notice, tt.wantNotice)
			}
		})
	}
}

func TestCanonicalizeReasoningDialectKeysDeleted(t *testing.T) {
	t.Run("all dialect keys removed, only canonical keys remain", func(t *testing.T) {
		body := map[string]any{
			"model":             "m",
			"reasoning":         map[string]any{"effort": "low"},
			"reasoning_effort":  "high",
			"enable_thinking":   false,
			"thinking":          map[string]any{"type": "enabled"},
			"include_reasoning": true,
			"thinking_budget":   999,
		}

		canonicalizeReasoning(body, model.RequestDefaults{})

		want := map[string]any{
			"model":            "m",
			"reasoning_effort": "none",
			"thinking_budget":  999,
		}
		if got := testJSON(t, body); got != testJSON(t, want) {
			t.Fatalf("body = %s, want %s", got, testJSON(t, want))
		}
	})

	t.Run("no reasoning input leaves body untouched", func(t *testing.T) {
		body := map[string]any{
			"model":       "m",
			"messages":    []any{map[string]any{"role": "user"}},
			"temperature": float64(0.7),
		}

		canonicalizeReasoning(body, model.RequestDefaults{})

		want := map[string]any{
			"model":       "m",
			"messages":    []any{map[string]any{"role": "user"}},
			"temperature": float64(0.7),
		}
		if got := testJSON(t, body); got != testJSON(t, want) {
			t.Fatalf("body = %s, want %s", got, testJSON(t, want))
		}
	})
}

func TestReasoningNormalizeEffortAliases(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "none", value: "none", want: "none"},
		{name: "minimal", value: "minimal", want: "minimal"},
		{name: "low", value: "low", want: "low"},
		{name: "medium", value: "medium", want: "medium"},
		{name: "high", value: "high", want: "high"},
		{name: "xhigh", value: "xhigh", want: "xhigh"},
		{name: "max", value: "max", want: "max"},
		{name: "ultra", value: "ultra", want: "ultra"},
		{name: "unknown", value: "mega", want: ""},
		{name: "non string", value: 42, want: ""},
		{name: "nil", value: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeReasoningEffort(tt.value); got != tt.want {
				t.Fatalf("normalizeReasoningEffort(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestReasoningIntValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "int", value: 512, want: 512},
		{name: "float64", value: float64(2048), want: 2048},
		{name: "int64", value: int64(4096), want: 4096},
		{name: "string", value: "512", want: 0},
		{name: "nil", value: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := intValue(tt.value); got != tt.want {
				t.Fatalf("intValue(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestApplyReasoningDialect(t *testing.T) {
	tests := []struct {
		name     string
		style    string
		body     map[string]any
		wantBody map[string]any
	}{
		{
			name:  "openai keeps only reasoning_effort",
			style: "openai",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
				"thinking_budget":  512,
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
			},
		},
		{
			name:  "openai drops budget and exclude",
			style: "openai",
			body: map[string]any{
				"model":             "m",
				"reasoning_effort":  "low",
				"thinking_budget":   512,
				"reasoning_exclude": true,
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
			},
		},
		{
			name:  "unknown style falls to openai branch",
			style: "somethingelse",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "medium",
			},
			wantBody: map[string]any{
				"model":            "m",
				"reasoning_effort": "medium",
			},
		},
		{
			name:  "openrouter builds reasoning map with effort and max_tokens",
			style: "openrouter",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
				"thinking_budget":  2048,
			},
			wantBody: map[string]any{
				"model": "m",
				"reasoning": map[string]any{
					"effort":     "high",
					"max_tokens": 2048,
				},
			},
		},
		{
			name:  "openrouter none becomes enabled false",
			style: "openrouter",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
			wantBody: map[string]any{
				"model": "m",
				"reasoning": map[string]any{
					"enabled": false,
				},
			},
		},
		{
			name:  "openrouter none with budget keeps enabled false and max_tokens",
			style: "openrouter",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
				"thinking_budget":  128,
			},
			wantBody: map[string]any{
				"model": "m",
				"reasoning": map[string]any{
					"enabled":    false,
					"max_tokens": 128,
				},
			},
		},
		{
			name:  "openrouter exclude only still builds reasoning map",
			style: "openrouter",
			body: map[string]any{
				"model":             "m",
				"reasoning_exclude": true,
			},
			wantBody: map[string]any{
				"model": "m",
				"reasoning": map[string]any{
					"exclude": true,
				},
			},
		},
		{
			name:  "openrouter empty canonical leaves body untouched",
			style: "openrouter",
			body: map[string]any{
				"model":  "m",
				"stream": true,
			},
			wantBody: map[string]any{
				"model":  "m",
				"stream": true,
			},
		},
		{
			name:  "qwen high becomes enable_thinking true",
			style: "qwen",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "high",
			},
			wantBody: map[string]any{
				"model":           "m",
				"enable_thinking": true,
			},
		},
		{
			name:  "qwen none becomes enable_thinking false",
			style: "qwen",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "none",
			},
			wantBody: map[string]any{
				"model":           "m",
				"enable_thinking": false,
			},
		},
		{
			name:  "qwen budget passes through with enable_thinking",
			style: "qwen",
			body: map[string]any{
				"model":            "m",
				"reasoning_effort": "low",
				"thinking_budget":  777,
			},
			wantBody: map[string]any{
				"model":           "m",
				"enable_thinking": true,
				"thinking_budget": 777,
			},
		},
		{
			name:  "qwen budget only omits enable_thinking",
			style: "qwen",
			body: map[string]any{
				"model":           "m",
				"thinking_budget": 777,
			},
			wantBody: map[string]any{
				"model":           "m",
				"thinking_budget": 777,
			},
		},
		{
			name:  "qwen drops exclude",
			style: "qwen",
			body: map[string]any{
				"model":             "m",
				"reasoning_effort":  "high",
				"reasoning_exclude": true,
			},
			wantBody: map[string]any{
				"model":           "m",
				"enable_thinking": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyReasoningDialect(tt.body, tt.style)

			if got, want := testJSON(t, tt.body), testJSON(t, tt.wantBody); got != want {
				t.Fatalf("body = %s, want %s", got, want)
			}
		})
	}
}
