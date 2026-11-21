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

// repFragmentStream is an upstream that streams two parallel execute calls as
// fragments — the native name only exists inside the completed arguments JSON.
func repFragmentStream(w http.ResponseWriter) {
	frames := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"execute","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"tool\":\"srv_alpha__list\",\"arg"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"uments\":{\"path\":\".\"}}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"execute","arguments":"{\"tool\":\"srv_beta__read\",\"arguments\":{\"path\":\"a.txt\"}}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, f := range frames {
		io.WriteString(w, "data: "+f+"\n\n")
	}
	io.WriteString(w, "data: [DONE]\n\n")
}

// repClientCalls folds the client-side SSE stream back into assembled calls.
func repClientCalls(t *testing.T, body string) []map[string]any {
	t.Helper()

	acc := map[int]map[string]any{}
	finish := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var evt struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			t.Fatalf("client frame not JSON: %v (%q)", err, payload)
		}
		for _, ch := range evt.Choices {
			if ch.FinishReason != nil {
				finish = *ch.FinishReason
			}
			for _, part := range ch.Delta.ToolCalls {
				m, ok := acc[part.Index]
				if !ok {
					m = map[string]any{"id": "", "name": "", "arguments": ""}
					acc[part.Index] = m
				}
				if part.ID != "" {
					m["id"] = part.ID
				}
				if part.Function.Name != "" {
					m["name"] = part.Function.Name
				}
				m["arguments"] = m["arguments"].(string) + part.Function.Arguments
			}
		}
	}
	if finish != "tool_calls" {
		t.Errorf("client finish_reason = %q, want tool_calls", finish)
	}

	calls := make([]map[string]any, 0, len(acc))
	for i := 0; i < len(acc); i++ {
		calls = append(calls, acc[i])
	}

	return calls
}

func TestForwardOpenCodeRepToolsRestoresStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		repFragmentStream(w)
	}))
	defer upstream.Close()

	t.Setenv("OPENCODE_REP_TOOLS", "1")
	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{"type":"function","function":{"name":"srv_alpha__list","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `"execute"`) {
		t.Errorf("client must not see the representative name:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("client stream lost its DONE marker")
	}

	calls := repClientCalls(t, body)
	if len(calls) != 2 {
		t.Fatalf("client saw %d calls, want 2 parallel native calls:\n%s", len(calls), body)
	}
	if calls[0]["name"] != "srv_alpha__list" || calls[1]["name"] != "srv_beta__read" {
		t.Errorf("native names wrong: %v / %v", calls[0]["name"], calls[1]["name"])
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0]["arguments"].(string)), &args); err != nil || args["path"] != "." {
		t.Errorf("call 0 arguments = %q (%v)", calls[0]["arguments"], err)
	}
	if calls[0]["id"] != "c1" || calls[1]["id"] != "c2" {
		t.Errorf("call ids lost: %v / %v", calls[0]["id"], calls[1]["id"])
	}
}

func TestForwardOpenCodeRepToolsRestoresBuffered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		repFragmentStream(w)
	}))
	defer upstream.Close()

	t.Setenv("OPENCODE_REP_TOOLS", "1")
	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))

	// stream:false — parseAndForceStream still streams upstream, but the client
	// receives the buffered JSON body.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"go"}],"tools":[{"type":"function","function":{"name":"srv_alpha__list","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("client body not JSON: %v\n%s", err, w.Body.String())
	}
	calls := result.Choices[0].Message.ToolCalls
	if len(calls) != 2 || calls[0].Function.Name != "srv_alpha__list" || calls[1].Function.Name != "srv_beta__read" {
		t.Errorf("buffered native calls wrong: %+v", calls)
	}
}
