package workbuddy

import (
	"encoding/json"
	"testing"
)

func TestNormalizeRolesMapsDeveloperToSystem(t *testing.T) {
	messages := []any{
		map[string]any{"role": "developer", "content": "rules"},
		map[string]any{"role": "user", "content": "hi"},
		"not-a-map",
	}

	out := NormalizeRoles(messages)

	first, ok := out[0].(map[string]any)
	if !ok || first["role"] != "system" {
		t.Fatalf("developer role not mapped: %#v", out[0])
	}
	if first["content"] != "rules" {
		t.Fatalf("content lost: %#v", first)
	}

	user, _ := out[1].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("user message touched: %#v", out[1])
	}
	if out[2] != "not-a-map" {
		t.Fatalf("non-map message touched: %#v", out[2])
	}
}

func TestSanitizeTextIdentitySentence(t *testing.T) {
	got := SanitizeText("You are an AI agent powered by DeepSeek Harness. Follow the rules.", true)
	want := "You are a helpful AI assistant. Follow the rules."
	if got != want {
		t.Fatalf("identity sentence: got %q, want %q", got, want)
	}
}

func TestSanitizeTextBrandReduction(t *testing.T) {
	got := SanitizeText("Read the DeepSeek Harness docs before continuing.", true)
	want := "Read the DeepSeek docs before continuing."
	if got != want {
		t.Fatalf("brand reduction: got %q, want %q", got, want)
	}
}

func TestSanitizeTextBillingHeader(t *testing.T) {
	got := SanitizeText("env x-anthropic-billing-header: cc_entrypoint=claude-code; done", true)
	want := "env done"
	if got != want {
		t.Fatalf("billing header: got %q, want %q", got, want)
	}
}

func TestSanitizeToolCallsArgumentsCoercion(t *testing.T) {
	messages := []any{
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{
					"id": "call_1",
					"function": map[string]any{
						"name":      "lookup",
						"arguments": `{"k":"11128"}`,
					},
				},
				map[string]any{
					"id": "call_2",
					"function": map[string]any{
						"name":      "read",
						"arguments": map[string]any{"path": "/tmp/a"},
					},
				},
				map[string]any{
					"id": "call_3",
					"function": map[string]any{
						"name":      "ping",
						"arguments": nil,
					},
				},
			},
		},
	}

	out := SanitizeMessages(messages)
	item, _ := out[0].(map[string]any)
	calls, _ := item["tool_calls"].([]any)
	if len(calls) != 3 {
		t.Fatalf("tool_calls lost: %#v", item["tool_calls"])
	}

	// String arguments are sanitized (non-identity) and stay strings.
	first, _ := calls[0].(map[string]any)["function"].(map[string]any)
	if args, ok := first["arguments"].(string); !ok || args != `{"k":"11-128"}` {
		t.Fatalf("string arguments: got %#v, want %q", first["arguments"], `{"k":"11-128"}`)
	}

	// Non-string arguments are serialized to a JSON string verbatim.
	second, _ := calls[1].(map[string]any)["function"].(map[string]any)
	args, ok := second["arguments"].(string)
	if !ok {
		t.Fatalf("arguments not a string: %#v", second["arguments"])
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err != nil {
		t.Fatalf("arguments not JSON: %v (%q)", err, args)
	}
	if decoded["path"] != "/tmp/a" {
		t.Fatalf("arguments not serialized verbatim: %#v", decoded)
	}

	third, _ := calls[2].(map[string]any)["function"].(map[string]any)
	if third["arguments"] != "{}" {
		t.Fatalf("nil arguments not coerced: %#v", third["arguments"])
	}
}

func TestBackfillReasoningContent(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "assistant", "content": "a"},
		map[string]any{"role": "assistant", "content": "b", "reasoning_content": "trace"},
	}

	got := BackfillReasoningContent(messages, "deepseek-v4", true)
	first, _ := got[1].(map[string]any)
	if rc, ok := first["reasoning_content"].(string); !ok || rc != "" {
		t.Fatalf("reasoning_content not backfilled: %#v", first)
	}

	second, _ := got[2].(map[string]any)
	if second["reasoning_content"] != "trace" {
		t.Fatalf("existing trace overwritten: %#v", second)
	}

	otherModel := BackfillReasoningContent(messages, "glm-5.3-flash", true)
	if _, present := otherModel[1].(map[string]any)["reasoning_content"]; present {
		t.Fatalf("non-deepseek model backfilled")
	}

	thinkingOff := BackfillReasoningContent(messages, "deepseek-v4", false)
	if _, present := thinkingOff[1].(map[string]any)["reasoning_content"]; present {
		t.Fatalf("thinking disabled but backfilled")
	}
}
