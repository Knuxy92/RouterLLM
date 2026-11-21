package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routerllm/internal/model"
)

func TestForwardOpenCodeRepToolsLeavesCLIExecuteUntouched(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		// A Code Mode CLI client's execute call: arguments are JS code, not the
		// representative wrapper — the response side must pass it through.
		frames := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c9","type":"function","function":{"name":"execute","arguments":"{\"code\":\"await tools"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":".ns.tool({})\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		writeSSEFrames(w, frames)
	}))
	defer upstream.Close()

	t.Setenv("OPENCODE_REP_TOOLS", "1")
	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{"type":"function","function":{"name":"execute","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	calls := repClientCalls(t, w.Body.String())
	if len(calls) != 1 {
		t.Fatalf("client saw %d calls, want 1:\n%s", len(calls), w.Body.String())
	}
	if calls[0]["name"] != "execute" {
		t.Errorf("CLI execute name rewritten: %q", calls[0]["name"])
	}
	if got := calls[0]["arguments"].(string); got != `{"code":"await tools.ns.tool({})"}` {
		t.Errorf("CLI execute arguments corrupted: %q", got)
	}
}

func TestForwardOpenCodeRepToolsKeepsLiveContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		// Content and a buffered execute fragment share one frame.
		frames := []string{
			`{"choices":[{"index":0,"delta":{"content":"calling "}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"now ","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"execute","arguments":"{\"tool\":\"srv_alpha__list\",\"arguments\":{\"path\":\".\"}}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		writeSSEFrames(w, frames)
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
	if !strings.Contains(body, `"calling "`) || !strings.Contains(body, `"now "`) {
		t.Errorf("live content lost next to a buffered fragment:\n%s", body)
	}
	calls := repClientCalls(t, body)
	if len(calls) != 1 || calls[0]["name"] != "srv_alpha__list" {
		t.Errorf("native call wrong after mixed frame: %v", calls)
	}
}

func TestRepRestoreBufferedUnwraps(t *testing.T) {
	result := &model.ChatCompletionResponse{Choices: []model.Choice{{
		Message: model.Message{ToolCalls: []model.ToolCall{
			{Index: 0, ID: "c1", Function: model.ToolCallFunction{Name: "execute", Arguments: `{"tool":"srv_alpha__list","arguments":{"path":"."}}`}},
			{Index: 1, ID: "c2", Function: model.ToolCallFunction{Name: "execute", Arguments: `{"code":"x"}`}},
			{Index: 2, ID: "c3", Function: model.ToolCallFunction{Name: "native_tool", Arguments: `{}`}},
		}},
	}}}

	out := repRestoreBuffered(result)
	calls := out.Choices[0].Message.ToolCalls
	if calls[0].Function.Name != "srv_alpha__list" || calls[0].Function.Arguments != `{"path":"."}` {
		t.Errorf("wrapper not unwrapped: %+v", calls[0].Function)
	}
	if calls[1].Function.Name != "execute" || calls[1].Function.Arguments != `{"code":"x"}` {
		t.Errorf("CLI execute touched: %+v", calls[1].Function)
	}
	if calls[2].Function.Name != "native_tool" {
		t.Errorf("foreign call touched: %+v", calls[2].Function)
	}
}

// writeSSEFrames writes pre-built SSE data frames plus the DONE marker.
func writeSSEFrames(w http.ResponseWriter, frames []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, f := range frames {
		_, _ = w.Write([]byte("data: " + f + "\n\n"))
	}
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

// The gateway folds the last call fragment and finish_reason into one frame;
// that frame must reach the client with its finish_reason intact even though
// every tool_call part in it was buffered.
func TestForwardOpenCodeRepToolsKeepsFinishReasonFoldedWithLastFragment(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		frames := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"execute","arguments":"{\"tool\":\"srv_alpha__list\",\"arg"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"uments\":{\"path\":\".\"}}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		}
		writeSSEFrames(w, frames)
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
	calls := repClientCalls(t, body) // also asserts finish_reason == tool_calls
	if len(calls) != 1 || calls[0]["name"] != "srv_alpha__list" {
		t.Errorf("native call wrong: %v\n%s", calls, body)
	}
}
