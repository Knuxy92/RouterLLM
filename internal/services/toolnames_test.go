package services

import (
	"strings"
	"testing"
)

func chatTool(name string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": "d",
			"parameters":  map[string]any{"type": "object"},
		},
	}
}

func flatTool(name string) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": "d",
	}
}

func toolDeclName(t *testing.T, entry any) string {
	t.Helper()

	name, ok := toolComparableName(entry)
	if !ok {
		t.Fatalf("entry has no comparable name: %v", entry)
	}

	return name
}

func TestToolNameShortUntouched(t *testing.T) {
	exact := strings.Repeat("s", 64)
	body := map[string]any{
		"model": "m",
		"tools": []any{chatTool("get_weather"), flatTool("search"), chatTool(exact)},
	}

	reverse := sanitizeToolNames(body)

	if len(reverse) != 0 {
		t.Fatalf("reverse = %v, want empty", reverse)
	}

	tools, _ := body["tools"].([]any)
	for i, want := range []string{"get_weather", "search", exact} {
		if got := toolDeclName(t, tools[i]); got != want {
			t.Fatalf("tools[%d] = %q, want %q", i, got, want)
		}
	}
}

func TestToolNameLongSanitizedDeterministic(t *testing.T) {
	long := strings.Repeat("a", 68)
	want := strings.Repeat("a", 56) + "-574c61e"

	if len(want) != 64 {
		t.Fatalf("hardcoded vector len = %d, want 64", len(want))
	}
	if got := sanitizeToolName(long); got != want {
		t.Fatalf("sanitizeToolName = %q, want %q", got, want)
	}
	if got := sanitizeToolName(long); got != want {
		t.Fatalf("second call = %q, want deterministic %q", got, want)
	}

	body := map[string]any{"model": "m", "tools": []any{chatTool(long)}}
	reverse := sanitizeToolNames(body)

	if reverse[want] != long {
		t.Fatalf("reverse = %v, want %q->%q", reverse, want, long)
	}

	tools, _ := body["tools"].([]any)
	if got := toolDeclName(t, tools[0]); got != want {
		t.Fatalf("declaration = %q, want %q", got, want)
	}
}

func TestToolNameCollisionFallbackUnique(t *testing.T) {
	t.Run("seeded used map forces fallback loop", func(t *testing.T) {
		long := strings.Repeat("b", 70)
		primary := sanitizeToolName(long)

		used := map[string]bool{primary: true}
		first := uniqueSanitizedToolName(long, used)

		if first == primary {
			t.Fatalf("fallback returned taken primary %q", first)
		}
		if len(first) != 64 {
			t.Fatalf("fallback len = %d, want 64", len(first))
		}
		if !strings.HasPrefix(first, strings.Repeat("b", 40)+"-") {
			t.Fatalf("fallback = %q, want name[:40] prefix", first)
		}

		used[first] = true
		if second := uniqueSanitizedToolName(long, used); second == primary || second == first {
			t.Fatalf("second fallback = %q, want fresh name", second)
		}

		if again := uniqueSanitizedToolName(long, map[string]bool{primary: true}); again != first {
			t.Fatalf("fallback not deterministic: %q vs %q", again, first)
		}
	})

	t.Run("declared short name wins over long primary", func(t *testing.T) {
		long := strings.Repeat("b", 70)
		primary := sanitizeToolName(long)

		body := map[string]any{"tools": []any{chatTool(primary), chatTool(long)}}
		reverse := sanitizeToolNames(body)

		tools, _ := body["tools"].([]any)
		if got := toolDeclName(t, tools[0]); got != primary {
			t.Fatalf("short declaration = %q, want untouched %q", got, primary)
		}

		san := toolDeclName(t, tools[1])
		if san == primary {
			t.Fatalf("colliding long kept taken name %q", san)
		}
		if len(san) != 64 {
			t.Fatalf("fallback len = %d, want 64", len(san))
		}
		if reverse[san] != long {
			t.Fatalf("reverse = %v, want %q->%q", reverse, san, long)
		}
	})
}

func TestToolNameSkipsEmptyAndNonFunction(t *testing.T) {
	other := map[string]any{"type": "web_search", "name": "web_search"}
	body := map[string]any{
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": ""}},
			map[string]any{"type": "function"},
			other,
			"plain",
			nil,
			other,
		},
	}
	before := testJSON(t, body["tools"])

	if dropped := dedupeTools(body); dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}

	if reverse := sanitizeToolNames(body); len(reverse) != 0 {
		t.Fatalf("reverse = %v, want empty", reverse)
	}

	if got := testJSON(t, body["tools"]); got != before {
		t.Fatalf("tools changed:\n got %s\nwant %s", got, before)
	}
}

func TestToolNameDedupeKeepsFirst(t *testing.T) {
	a1 := chatTool("alpha")
	a1["function"].(map[string]any)["parameters"] = map[string]any{"v": 1}
	a2 := chatTool("alpha")
	a2["function"].(map[string]any)["parameters"] = map[string]any{"v": 2}

	body := map[string]any{"tools": []any{
		a1, chatTool("beta"), a2, flatTool("gamma"), flatTool("gamma"), chatTool("Get"), chatTool("get"),
	}}

	dropped := dedupeTools(body)

	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}

	tools, _ := body["tools"].([]any)
	for i, want := range []string{"alpha", "beta", "gamma", "Get", "get"} {
		if got := toolDeclName(t, tools[i]); got != want {
			t.Fatalf("tools[%d] = %q, want %q", i, got, want)
		}
	}

	params, _ := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	if params["v"] != 1 {
		t.Fatalf("kept alpha params = %v, want first entry", params)
	}
}

func TestToolNameDedupeBeforeSanitize(t *testing.T) {
	long := strings.Repeat("c", 68)
	body := map[string]any{"tools": []any{chatTool(long), chatTool(long), chatTool("other")}}

	reverse, dropped := processToolNames(body, true, true)

	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(tools))
	}

	san := toolDeclName(t, tools[0])
	if len(san) != 64 {
		t.Fatalf("sanitized len = %d, want 64", len(san))
	}
	if reverse[san] != long {
		t.Fatalf("reverse = %v, want %q->%q", reverse, san, long)
	}
}

func TestToolNameRewritesChoiceAndHistory(t *testing.T) {
	long := "tool_" + strings.Repeat("x", 70)
	body := map[string]any{
		"model": "m",
		"tools": []any{chatTool(long), chatTool("short")},
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": long, "arguments": "{}"}},
				map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": "short", "arguments": "{}"}},
				map[string]any{"id": "call_3", "type": "function", "function": map[string]any{"name": "", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
		},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": long}},
		"toolConfig":  map[string]any{"functionCallingConfig": map[string]any{"allowed_function_names": []any{long, "short"}}},
	}

	reverse := sanitizeToolNames(body)

	tools, _ := body["tools"].([]any)
	san := toolDeclName(t, tools[0])
	if len(san) != 64 || san == long {
		t.Fatalf("declaration = %q, want 64-char rewrite", san)
	}
	if reverse[san] != long {
		t.Fatalf("reverse = %v, want %q->%q", reverse, san, long)
	}

	msgs, _ := body["messages"].([]any)
	calls, _ := msgs[1].(map[string]any)["tool_calls"].([]any)
	callName := func(i int) string {
		return calls[i].(map[string]any)["function"].(map[string]any)["name"].(string)
	}
	if got := callName(0); got != san {
		t.Fatalf("history call = %q, want %q", got, san)
	}
	if got := callName(1); got != "short" {
		t.Fatalf("history call = %q, want untouched short", got)
	}
	if got := callName(2); got != "" {
		t.Fatalf("history call = %q, want empty skipped", got)
	}
	if id := calls[0].(map[string]any)["id"]; id != "call_1" {
		t.Fatalf("history id = %v, want untouched", id)
	}
	if toolCallID := msgs[2].(map[string]any)["tool_call_id"]; toolCallID != "call_1" {
		t.Fatalf("tool_call_id = %v, want untouched", toolCallID)
	}

	choice, _ := body["tool_choice"].(map[string]any)["function"].(map[string]any)
	if choice["name"] != san {
		t.Fatalf("tool_choice = %v, want %q", choice["name"], san)
	}

	allowed, _ := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["allowed_function_names"].([]any)
	if allowed[0] != san || allowed[1] != "short" {
		t.Fatalf("allowed_function_names = %v, want [%q short]", allowed, san)
	}
}

func TestToolNameRoundTripRestore(t *testing.T) {
	long := strings.Repeat("d", 76)
	flat := strings.Repeat("e", 70)
	body := map[string]any{
		"model": "m",
		"tools": []any{chatTool(long), flatTool(flat), chatTool("short")},
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": long, "arguments": "{}"}},
				map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": flat, "arguments": "{}"}},
			}},
		},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": long}},
		"toolConfig":  map[string]any{"functionCallingConfig": map[string]any{"allowed_function_names": []any{flat, "short"}}},
	}
	before := testJSON(t, body)

	reverse, dropped := processToolNames(body, false, true)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(reverse) != 2 {
		t.Fatalf("reverse len = %d, want 2", len(reverse))
	}

	restoreToolNames(body, reverse)

	if got := testJSON(t, body); got != before {
		t.Fatalf("round trip mismatch:\n got %s\nwant %s", got, before)
	}
}

func TestToolNameBothOffByteIdentical(t *testing.T) {
	long := strings.Repeat("f", 68)
	body := map[string]any{
		"tools":       []any{chatTool(long), chatTool(long)},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": long}},
	}
	before := testJSON(t, body)

	reverse, dropped := processToolNames(body, false, false)

	if reverse != nil {
		t.Fatalf("reverse = %v, want nil", reverse)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if got := testJSON(t, body); got != before {
		t.Fatalf("body changed:\n got %s\nwant %s", got, before)
	}
}

func TestToolNameProcessFlags(t *testing.T) {
	t.Run("dedupe only leaves names alone", func(t *testing.T) {
		body := map[string]any{"tools": []any{chatTool("a"), chatTool("a")}}

		reverse, dropped := processToolNames(body, true, false)

		if reverse != nil {
			t.Fatalf("reverse = %v, want nil", reverse)
		}
		if dropped != 1 {
			t.Fatalf("dropped = %d, want 1", dropped)
		}

		tools, _ := body["tools"].([]any)
		if got := toolDeclName(t, tools[0]); got != "a" {
			t.Fatalf("declaration = %q, want untouched", got)
		}
	})

	t.Run("sanitize only keeps duplicates mapped alike", func(t *testing.T) {
		long := strings.Repeat("g", 68)
		body := map[string]any{"tools": []any{chatTool(long), chatTool(long)}}

		reverse, dropped := processToolNames(body, false, true)

		if dropped != 0 {
			t.Fatalf("dropped = %d, want 0", dropped)
		}
		if len(reverse) != 1 {
			t.Fatalf("reverse len = %d, want 1", len(reverse))
		}

		tools, _ := body["tools"].([]any)
		first, second := toolDeclName(t, tools[0]), toolDeclName(t, tools[1])
		if first != second || len(first) != 64 {
			t.Fatalf("declarations = %q, %q, want identical 64-char rewrites", first, second)
		}
	})
}
