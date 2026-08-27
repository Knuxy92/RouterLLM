package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicRequestToOpenAIFlattensClaudeCodeSystemBlocks(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.f11; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Be helpful.","cache_control":{"type":"ephemeral"}}],"tools":[],"metadata":{"user_id":"abc"},"max_tokens":32000,"stream":true}`

	got, err := AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}

	if _, ok := body["system"]; ok {
		t.Error("system field should be removed")
	}

	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("messages should be an array")
	}

	first, _ := msgs[0].(map[string]any)
	if first == nil || first["role"] != "system" {
		t.Fatal("first message role should be system")
	}

	content, ok := first["content"].(string)
	if !ok {
		t.Fatal("system message content should be a string")
	}
	for _, want := range []string{"x-anthropic-billing-header", "You are Claude Code", "Be helpful"} {
		if !strings.Contains(content, want) {
			t.Errorf("system content missing %q", want)
		}
	}

	second, _ := msgs[1].(map[string]any)
	userContent, ok := second["content"].(string)
	if !ok {
		t.Fatal("user message content should be a string")
	}
	if userContent != "Hello" {
		t.Errorf("user content = %q, want Hello", userContent)
	}

	for _, key := range []string{"tools", "metadata"} {
		if _, ok := body[key]; ok {
			t.Errorf("field %q should not exist", key)
		}
	}
}

func TestAnthropicRequestToOpenAIMergesSystemRoleMessage(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]},{"role":"system","content":[{"type":"text","text":"Tools available.","cache_control":{"type":"ephemeral"}}]}],"system":[{"type":"text","text":"You are Claude Code."}],"max_tokens":32000}`

	got, err := AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}

	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("messages should be an array")
	}
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2", len(msgs))
	}

	first, _ := msgs[0].(map[string]any)
	if first == nil || first["role"] != "system" {
		t.Fatal("first message should be system")
	}
	content, _ := first["content"].(string)
	for _, want := range []string{"You are Claude Code", "Tools available"} {
		if !strings.Contains(content, want) {
			t.Errorf("system content missing %q", want)
		}
	}
}

func TestAnthropicRequestToOpenAIJoinsMultipleTextBlocks(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>"},{"type":"text","text":"Hey Claude"}]}],"max_tokens":32000}`

	got, err := AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}

	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatal("messages should be a non-empty array")
	}
	msg, _ := msgs[0].(map[string]any)
	content, _ := msg["content"].(string)

	for _, want := range []string{"<system-reminder>", "Hey Claude"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q", want)
		}
	}
}
