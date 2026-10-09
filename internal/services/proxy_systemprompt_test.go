package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

const testSystemPrompt = "SYSTEM-PROMPT-FROM-CONFIG"

// systemPromptProxy serves one openai-style upstream and records the last body it
// received, so the injected prompt can be asserted on the wire.
func systemPromptProxy(t *testing.T, upstream *httptest.Server, prompt string) (*Proxy, *map[string]any) {
	t.Helper()

	var captured map[string]any
	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "sp-test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"sk-test"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "sp-test", Model: "up-model"}},
	}}, time.Minute)

	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, prompt)
	_ = captured

	return proxy, &captured
}

func capturingUpstream(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("body decode: %v", err)
		}
		*body = decoded

		w.Header().Set("Content-Type", "application/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)

	return upstream
}

func forwardBody(t *testing.T, p *Proxy, path, requestBody string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(requestBody))
	w := httptest.NewRecorder()

	p.Forward(path, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemPromptReachesResponsesClients(t *testing.T) {
	var upstream map[string]any
	upstreamServer := capturingUpstream(t, &upstream)
	proxy, _ := systemPromptProxy(t, upstreamServer, testSystemPrompt)

	forwardBody(t, proxy, "/v1/responses", `{"model":"test-model","input":"hi"}`)

	instructions, _ := upstream["instructions"].(string)
	if instructions != testSystemPrompt {
		t.Fatalf("instructions = %q, want the configured system prompt (responses bodies carry input, not messages)", instructions)
	}
}

func TestSystemPromptKeepsClientInstructions(t *testing.T) {
	var upstream map[string]any
	upstreamServer := capturingUpstream(t, &upstream)
	proxy, _ := systemPromptProxy(t, upstreamServer, testSystemPrompt)

	forwardBody(t, proxy, "/v1/responses", `{"model":"test-model","instructions":"CLIENT-INSTRUCTIONS","input":"hi"}`)

	instructions, _ := upstream["instructions"].(string)
	if !strings.Contains(instructions, testSystemPrompt) {
		t.Errorf("instructions = %q, want it to carry the configured prompt", instructions)
	}
	if !strings.Contains(instructions, "CLIENT-INSTRUCTIONS") {
		t.Errorf("instructions = %q, want the client's own instructions preserved", instructions)
	}
	if strings.Index(instructions, testSystemPrompt) > strings.Index(instructions, "CLIENT-INSTRUCTIONS") {
		t.Errorf("instructions = %q, want the configured prompt first so it leads", instructions)
	}
}

func TestSystemPromptSkipsWhenClientSentSystemMessage(t *testing.T) {
	var upstream map[string]any
	upstreamServer := capturingUpstream(t, &upstream)
	proxy, _ := systemPromptProxy(t, upstreamServer, testSystemPrompt)

	forwardBody(t, proxy, "/v1/chat/completions",
		`{"model":"test-model","messages":[{"role":"system","content":"CLIENT-SYSTEM"},{"role":"user","content":"hi"}]}`)

	messages, _ := upstream["messages"].([]any)
	systems := 0
	for _, entry := range messages {
		msg, _ := entry.(map[string]any)
		if role, _ := msg["role"].(string); role == "system" || role == "developer" {
			systems++
		}
	}
	if systems != 1 {
		t.Fatalf("system messages upstream = %d, want 1 — the client already has one: %v", systems, messages)
	}
	if content, _ := messages[0].(map[string]any)["content"].(string); content != "CLIENT-SYSTEM" {
		t.Errorf("first message content = %q, want the client's own system message untouched", content)
	}
}

func TestSystemPromptStillPrependsWhenClientHasNone(t *testing.T) {
	var upstream map[string]any
	upstreamServer := capturingUpstream(t, &upstream)
	proxy, _ := systemPromptProxy(t, upstreamServer, testSystemPrompt)

	forwardBody(t, proxy, "/v1/chat/completions", `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)

	messages, _ := upstream["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want the injected system message plus the client's", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != testSystemPrompt {
		t.Fatalf("first message = %v, want the configured prompt as system", first)
	}
}

func TestSystemPromptNoopWhenUnset(t *testing.T) {
	var upstream map[string]any
	upstreamServer := capturingUpstream(t, &upstream)
	proxy, _ := systemPromptProxy(t, upstreamServer, "")

	forwardBody(t, proxy, "/v1/responses", `{"model":"test-model","input":"hi"}`)

	if _, ok := upstream["instructions"]; ok {
		t.Errorf("instructions = %v, want none when system_prompt_file is unset", upstream["instructions"])
	}
}