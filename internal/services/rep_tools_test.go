package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// repTestBody builds a chat body with two client tools, an assistant turn that
// already called them natively, and the matching tool results — the shape a
// dumb client sends on the round after the response side restored native names.
func repTestBody() map[string]any {
	return map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "system", "content": "base"},
			map[string]any{"role": "user", "content": "go"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{
					"name": "srv_alpha__list", "arguments": `{"path":"."}`,
				}},
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{
					"name": "srv_beta__read", "arguments": `{"path":"a.txt"}`,
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": `{"ok":true}`},
			map[string]any{"role": "tool", "tool_call_id": "c2", "content": `{"ok":true}`},
		},
		"tools": []any{chatTool("srv_alpha__list"), chatTool("srv_beta__read")},
	}
}

func repUpstreamToolNames(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	tools, _ := body["tools"].([]any)
	for _, entry := range tools {
		if tm, ok := entry.(map[string]any); ok {
			if fn, ok := tm["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); name != "" {
					names[name] = true
				}
			}
		}
	}

	return names
}

func TestCollapseRepToolsCollapsesDeclarationsAndHistory(t *testing.T) {
	body := repTestBody()

	collapsed, ok := collapseRepTools(body)
	if !ok {
		t.Fatal("collapse did not apply")
	}
	if collapsed != 2 {
		t.Errorf("collapsed = %d, want 2", collapsed)
	}

	names := repUpstreamToolNames(t, body)
	if len(names) != 1 || !names[repToolName] {
		t.Errorf("tools must shrink to the representative execute tool, got %v", names)
	}

	msgs := body["messages"].([]any)
	if first, _ := msgs[0].(map[string]any); first == nil {
		t.Fatal("messages[0] missing")
	} else if content, _ := first["content"].(string); !strings.Contains(content, "## Tool catalog") ||
		!strings.Contains(content, "srv_alpha__list") || !strings.Contains(content, "srv_beta__read") {
		t.Errorf("catalog missing from the leading system message:\n%.200s", content)
	}

	assistant := msgs[2].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	for i, want := range []string{"srv_alpha__list", "srv_beta__read"} {
		fn := calls[i].(map[string]any)["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != repToolName {
			t.Errorf("history call %d name = %q, want %q", i, name, repToolName)
		}
		var wrapper struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		raw, _ := fn["arguments"].(string)
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			t.Fatalf("history call %d arguments not representative JSON: %v (%q)", i, err, raw)
		}
		if wrapper.Tool != want {
			t.Errorf("history call %d wrapped tool = %q, want %q", i, wrapper.Tool, want)
		}
	}

	if _, isTool := msgs[3].(map[string]any); isTool {
		if role, _ := msgs[3].(map[string]any)["role"].(string); role != "tool" {
			t.Errorf("tool result message mutated: role %q", role)
		}
	}
}

func TestCollapseRepToolsSkips(t *testing.T) {
	t.Run("client already speaks code mode", func(t *testing.T) {
		body := repTestBody()
		body["tools"] = append(body["tools"].([]any), chatTool("execute"))
		if _, ok := collapseRepTools(body); ok {
			t.Error("collapse applied to a code-mode client")
		}
	})
	t.Run("no tools", func(t *testing.T) {
		body := repTestBody()
		delete(body, "tools")
		if _, ok := collapseRepTools(body); ok {
			t.Error("collapse applied without tools")
		}
	})
	t.Run("tool_choice none", func(t *testing.T) {
		body := repTestBody()
		body["tool_choice"] = "none"
		if _, ok := collapseRepTools(body); ok {
			t.Error("collapse applied when the client forbids tool use")
		}
	})
	t.Run("no messages", func(t *testing.T) {
		body := repTestBody()
		delete(body, "messages")
		if _, ok := collapseRepTools(body); ok {
			t.Error("collapse applied without messages")
		}
	})
}

func TestCollapseRepToolsUsesSanitizedNames(t *testing.T) {
	body := repTestBody()
	long := strings.Repeat("s", 70)
	body["tools"] = append(body["tools"].([]any), chatTool(long))

	// The leg pipeline sanitizes before the collapse hook runs; mirror it.
	processToolNames(body, false, true)

	if _, ok := collapseRepTools(body); !ok {
		t.Fatal("collapse did not apply")
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, sanitizeToolName(long)) {
		t.Errorf("catalog must carry the sanitized form the model will send back:\n%.120s", content)
	}
}

// repCollapsesUpstream asserts, inside a fake upstream, the outbound shape
// representative-tool mode must produce.
func repCollapsesUpstream(t *testing.T, body map[string]any) {
	t.Helper()

	names := repUpstreamToolNames(t, body)
	for _, want := range []string{"execute", "edit", "read", "shell", "write"} {
		if !names[want] {
			t.Errorf("upstream tool %q missing; names = %v", want, names)
		}
	}
	if len(names) != 5 {
		t.Errorf("upstream tools = %v, want the representative + required pack", names)
	}

	msgs, _ := body["messages"].([]any)
	found := false
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if role, _ := msg["role"].(string); role != "system" {
			continue
		}
		if content, _ := msg["content"].(string); strings.Contains(content, "## Tool catalog") &&
			strings.Contains(content, "srv_alpha__list") {
			found = true
		}
	}
	if !found {
		t.Error("catalog missing from the upstream system messages")
	}

	assistant := msgs[2].(map[string]any)
	fn := assistant["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if name, _ := fn["name"].(string); name != "execute" {
		t.Errorf("upstream history call name = %q, want execute", name)
	}
	var wrapper struct {
		Tool string `json:"tool"`
	}
	raw, _ := fn["arguments"].(string)
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil || wrapper.Tool != "srv_alpha__list" {
		t.Errorf("upstream history arguments = %q (%v), want wrapped srv_alpha__list", raw, err)
	}
}

func TestForwardOpenCodeRepToolsCollapsesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		repCollapsesUpstream(t, body)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"done"},"index":0}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	t.Setenv("OPENCODE_REP_TOOLS", "1")
	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"system","content":"base"},{"role":"user","content":"go"},`+
			`{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"srv_alpha__list","arguments":"{\"path\":\".\"}"}}]},`+
			`{"role":"tool","tool_call_id":"c1","content":"{}"}],"tools":[{"type":"function","function":{"name":"srv_alpha__list","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "done") {
		t.Errorf("client lost the answer: %s", w.Body.String())
	}
}

func TestForwardOpenCodeRepToolsOffKeepsNativeDeclarations(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		names := repUpstreamToolNames(t, body)
		if !names["srv_alpha__list"] {
			t.Errorf("native declaration must survive with the mode off; names = %v", names)
		}
		if names["execute"] {
			t.Errorf("representative tool must not appear with the mode off; names = %v", names)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"done"},"index":0}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))
	proxy.log = log.New(io.Discard, "", 0)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"go"}],`+
			`"tools":[{"type":"function","function":{"name":"srv_alpha__list","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}
