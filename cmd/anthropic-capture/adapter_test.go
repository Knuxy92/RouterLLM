package main

import (
	"encoding/json"
	"strings"
	"testing"

	"routerllm/internal/adapter"
)

func TestTranslateClaudeCodeToOpenAI(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.f11; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Be helpful.","cache_control":{"type":"ephemeral"}}],"tools":[],"metadata":{"user_id":"abc"},"max_tokens":32000,"stream":true}`

	got, err := adapter.AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	json.Unmarshal(got, &body)

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
	if !strings.Contains(content, "x-anthropic-billing-header") {
		t.Error("missing billing header")
	}
	if !strings.Contains(content, "You are Claude Code") {
		t.Error("missing Claude Code identity")
	}
	if !strings.Contains(content, "Be helpful") {
		t.Error("missing helpful instruction")
	}

	second, _ := msgs[1].(map[string]any)
	content2, ok := second["content"].(string)
	if !ok {
		t.Fatal("user message content should be a string")
	}
	if content2 != "Hello" {
		t.Errorf("user content = %q, want Hello", content2)
	}

	for _, key := range []string{"tools", "metadata"} {
		if _, ok := body[key]; ok {
			t.Errorf("field %q should not exist", key)
		}
	}
}

func TestTranslateWithSystemRoleMessage(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]},{"role":"system","content":[{"type":"text","text":"Tools available.","cache_control":{"type":"ephemeral"}}]}],"system":[{"type":"text","text":"You are Claude Code."}],"max_tokens":32000}`

	got, err := adapter.AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	json.Unmarshal(got, &body)
	msgs := body["messages"].([]any)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatal("first message should be system")
	}
	content := first["content"].(string)
	if !strings.Contains(content, "You are Claude Code") {
		t.Error("missing top-level system")
	}
	if !strings.Contains(content, "Tools available") {
		t.Error("missing role:system message")
	}
}

func TestTranslateWithContentBlocks(t *testing.T) {
	raw := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>"},{"type":"text","text":"Hey Claude"}]}],"max_tokens":32000}`

	got, err := adapter.AnthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	json.Unmarshal(got, &body)
	msgs := body["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].(string)

	if !strings.Contains(content, "<system-reminder>") {
		t.Error("missing text block 1")
	}
	if !strings.Contains(content, "Hey Claude") {
		t.Error("missing text block 2")
	}
}
