package adapter

import (
	"encoding/json"
	"testing"
)

func translateForTest(t *testing.T, body map[string]any) (map[string]any, string) {
	t.Helper()

	data, path, err := TranslateRequest(body, "test-model")
	if err != nil {
		t.Fatalf("TranslateRequest error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", string(data), err)
	}

	return out, path
}

func TestTranslateRequestThinkingCanonicalReasoning(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]any
		wantThinking map[string]any
		wantMaxTok   float64
	}{
		{
			name: "explicit max_tokens clamps budget to max_tokens-1",
			body: map[string]any{
				"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
				"reasoning_effort": "high",
				"thinking_budget":  32000.0,
				"max_tokens":       4096.0,
			},
			wantThinking: map[string]any{"type": "enabled", "budget_tokens": 4095.0},
			wantMaxTok:   4096,
		},
		{
			name: "no max_tokens lifts max_tokens to budget+1024",
			body: map[string]any{
				"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
				"reasoning_effort": "max",
				"thinking_budget":  32000.0,
			},
			wantThinking: map[string]any{"type": "enabled", "budget_tokens": 32000.0},
			wantMaxTok:   33024,
		},
		{
			name:         "reasoning_effort none omits thinking entirely",
			body:         map[string]any{"reasoning_effort": "none"},
			wantThinking: nil,
			wantMaxTok:   4096,
		},
		{
			name:         "effort without budget emits bare enabled thinking",
			body:         map[string]any{"reasoning_effort": "high"},
			wantThinking: map[string]any{"type": "enabled"},
			wantMaxTok:   4096,
		},
		{
			name: "legacy thinking dialect without reasoning_effort is ignored",
			body: map[string]any{
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 1234.0},
			},
			wantThinking: nil,
			wantMaxTok:   4096,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _ := translateForTest(t, tt.body)

			got, hasThinking := out["thinking"]
			if tt.wantThinking == nil {
				if hasThinking {
					t.Fatalf("expected no thinking key, got %v in JSON: %s", got, rawJSON(t, out))
				}
			} else {
				if !hasThinking {
					t.Fatalf("expected thinking %v, missing in JSON: %s", tt.wantThinking, rawJSON(t, out))
				}

				gotMap, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("thinking not an object: %T in JSON: %s", got, rawJSON(t, out))
				}

				if len(gotMap) != len(tt.wantThinking) {
					t.Fatalf("thinking keys mismatch: got %v, want %v in JSON: %s", gotMap, tt.wantThinking, rawJSON(t, out))
				}

				for k, want := range tt.wantThinking {
					if gotMap[k] != want {
						t.Fatalf("thinking[%q] = %v, want %v in JSON: %s", k, gotMap[k], want, rawJSON(t, out))
					}
				}
			}

			if gotMax, ok := out["max_tokens"].(float64); !ok || gotMax != tt.wantMaxTok {
				t.Fatalf("max_tokens = %v, want %v in JSON: %s", out["max_tokens"], tt.wantMaxTok, rawJSON(t, out))
			}
		})
	}
}

func TestTranslateRequestPath(t *testing.T) {
	out, path := translateForTest(t, map[string]any{"reasoning_effort": "high"})

	if path != "/v1/messages" {
		t.Fatalf("path = %q, want %q", path, "/v1/messages")
	}

	if _, ok := out["thinking"]; !ok {
		t.Fatalf("expected thinking key, JSON: %s", rawJSON(t, out))
	}
}

func rawJSON(t *testing.T, v any) string {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}

	return string(data)
}
