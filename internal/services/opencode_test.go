package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/opencode"
	"routerllm/internal/provider"
)

func opencodeTestRegistry(upstream *httptest.Server) *provider.Registry {
	return opencodeTestRegistryWithStyleCall(upstream, "")
}

func opencodeTestRegistryWithStyleCall(upstream *httptest.Server, styleCall string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "oc-test",
		BaseURL: upstream.URL,
		Style:   "opencode",
		Keys:    []string{"oc_refresh_token"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "oc-test", Model: "up-model", StyleCall: styleCall}},
	}}, time.Minute)
}

// openCodeSanitizedBash computes the deterministic renamed form a client tool
// named "bash" receives on an opencode leg.
func openCodeSanitizedBash() string {
	used := map[string]bool{}
	for _, name := range opencode.RequiredToolNames() {
		used[name] = true
	}

	return uniqueSanitizedToolName("bash", used)
}

func assertOpenCodeHeaders(t *testing.T, r *http.Request) {
	t.Helper()

	if got := r.Header.Get("User-Agent"); got != opencode.UserAgent {
		t.Errorf("User-Agent = %q, want %q", got, opencode.UserAgent)
	}
	for _, h := range []struct{ name, prefix string }{
		{"x-opencode-session", "ses_"},
		{"x-session-id", "ses_"},
		{"x-opencode-session-id", "ses_"},
		{"x-opencode-request", "msg_"},
	} {
		got := r.Header.Get(h.name)
		body := strings.TrimPrefix(got, h.prefix)
		if !strings.HasPrefix(got, h.prefix) || len(body) != 26 || strings.ToLower(body) != body || !isHex(body) {
			t.Errorf("%s = %q, want %s + 26 lowercase hex chars", h.name, got, h.prefix)
		}
	}
	if got := r.Header.Get("x-opencode-project"); got != "global" {
		t.Errorf("x-opencode-project = %q, want global", got)
	}
	if got := r.Header.Get("x-opencode-client"); got != "cli" {
		t.Errorf("x-opencode-client = %q, want cli", got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer oc_access_token" {
		t.Errorf("Authorization = %q, want Bearer oc_access_token", got)
	}
	if got := r.Header.Get("x-api-key"); got != "" {
		t.Errorf("x-api-key = %q, want empty (opencode auth is Bearer-only)", got)
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}

	return len(s) > 0
}

func assertRequiredToolsPresent(t *testing.T, tools []any) {
	t.Helper()

	byName := map[string]bool{}
	for _, entry := range tools {
		tm, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := tm["name"].(string); name != "" {
			byName[name] = true
			if name == "bash" {
				if strict, ok := tm["strict"].(bool); !ok || strict {
					t.Errorf("injected tool %q must carry strict=false (got %v, present %v)", name, strict, ok)
				}
			}
		}
	}
	for _, name := range opencode.RequiredToolNames() {
		if !byName[name] {
			t.Errorf("required tool %q missing from upstream tools", name)
		}
	}
}

// newOpencodeTestProxy points the account store and the Console server at the
// fake upstream, so the legacy tests exercise the account-token path without
// touching the network. The account holds no org, so no x-org-id is expected.
func newOpencodeTestProxy(t *testing.T, upstream *httptest.Server, registry *provider.Registry) *Proxy {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "opencode-accounts.json")
	t.Setenv("OPENCODE_ACCOUNTS_FILE", storePath)
	store, err := opencode.LoadAccountStore(storePath)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}
	if err := store.Add(opencode.Account{
		AccountID: "u-1", Email: "dev@example.test", RefreshToken: "oc_refresh_token",
		Server: upstream.URL, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.opencodeMu.Lock()
	proxy.opencodeManagers[upstream.URL] = opencode.NewManager(&opencode.Client{
		HTTPClient: upstream.Client(), Server: upstream.URL,
	}, store)
	proxy.opencodeMu.Unlock()

	return proxy
}

// opencodeTokenExchange serves the Console refresh endpoint with a long-lived
// access token, so the legacy tests pay exactly one exchange per run. Any
// other path falls through to the test's own handler body.
func opencodeTokenExchange(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/auth/device/token") {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  "oc_access_token",
		"refresh_token": "oc_refresh_token",
		"expires_in":    3600,
	})
}

func TestForwardOpenCodeInjectsToolsAndRenames(t *testing.T) {
	sanitized := openCodeSanitizedBash()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q, want /v1/responses", r.URL.Path)
			return
		}
		assertOpenCodeHeaders(t, r)

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != len(opencode.RequiredToolNames())+1 {
			t.Errorf("upstream tools = %d, want %d (7 required + 1 renamed client tool)", len(tools), len(opencode.RequiredToolNames())+1)
		}
		assertRequiredToolsPresent(t, tools)

		names := map[string]bool{}
		for _, entry := range tools {
			if tm, ok := entry.(map[string]any); ok {
				if name, _ := tm["name"].(string); name != "" {
					names[name] = true
				}
			}
		}
		if !names[sanitized] {
			t.Errorf("client tool not renamed to %q; names = %v", sanitized, names)
		}
		if names["bash"] && !names[sanitized] {
			t.Error("client tool kept the reserved name bash")
		}
		if _, ok := body["tool_choice"]; !ok {
			t.Error("tool_choice missing")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\""+sanitized+"\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "responses"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],`+
			`"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"name":"bash"`) {
		t.Fatalf("client must see the original tool name:\n%s", body)
	}
	if strings.Contains(body, sanitized) {
		t.Fatalf("client must not see the renamed tool:\n%s", body)
	}
}

func TestForwardOpenCodeResponsesInbound(t *testing.T) {
	sanitized := openCodeSanitizedBash()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		assertOpenCodeHeaders(t, r)

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		assertRequiredToolsPresent(t, body["tools"].([]any))

		input, _ := body["input"].([]any)
		found := false
		for _, item := range input {
			tm, ok := item.(map[string]any)
			if !ok || tm["type"] != "function_call" {
				continue
			}
			found = true
			if name, _ := tm["name"].(string); name != sanitized {
				t.Errorf("input function_call name = %q, want %q", name, sanitized)
			}
		}
		if !found {
			t.Errorf("history function_call missing from input: %v", body["input"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\""+sanitized+"\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "responses"))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"test-model","stream":true,"input":[{"type":"function_call","call_id":"call_0","name":"bash","arguments":"{}"}],`+
			`"tools":[{"type":"function","name":"bash","strict":false}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/responses", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"name":"bash"`) {
		t.Fatalf("client must see the original tool name:\n%s", body)
	}
	if strings.Contains(body, sanitized) {
		t.Fatalf("client must not see the renamed tool:\n%s", body)
	}
}

func TestForwardOpenCodeWithoutClientTools(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != len(opencode.RequiredToolNames()) {
			t.Errorf("upstream tools = %d, want exactly the %d required tools", len(tools), len(opencode.RequiredToolNames()))
		}
		assertRequiredToolsPresent(t, tools)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "responses"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("content missing:\n%s", w.Body.String())
	}
}

func TestForwardOpenCodeStyleCallChat(t *testing.T) {
	sanitized := openCodeSanitizedBash()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		assertOpenCodeHeaders(t, r)

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != len(opencode.RequiredToolNames())+1 {
			t.Errorf("upstream tools = %d, want %d", len(tools), len(opencode.RequiredToolNames())+1)
		}
		foundSanitized := false
		for _, entry := range tools {
			tm, _ := entry.(map[string]any)
			fn, _ := tm["function"].(map[string]any)
			if fn["name"] == sanitized {
				foundSanitized = true
			}
		}
		if !foundSanitized {
			t.Errorf("client tool not renamed to %q", sanitized)
		}
		if _, ok := body["stream_options"]; !ok {
			t.Error("stream_options.include_usage missing on chat dialect")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\""+sanitized+"\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "chat"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],`+
			`"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"name":"bash"`) {
		t.Fatalf("client must see the original tool name:\n%s", w.Body.String())
	}
}

func TestForwardOpenCodeDropsToolsForToolChoiceNone(t *testing.T) {
	var upstreamBody map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Errorf("body decode: %v", err)
		}
		assertOpenCodeHeaders(t, r)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "chat"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],"tool_choice":"none",`+
			`"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if _, ok := upstreamBody["tools"]; ok {
		t.Errorf("upstream tools = %v, want none when the client asked for no tool use", upstreamBody["tools"])
	}
	if _, ok := upstreamBody["tool_choice"]; ok {
		t.Errorf("upstream tool_choice = %v, want the field removed", upstreamBody["tool_choice"])
	}
}

func TestForwardOpenCodeForcesAutoToolChoice(t *testing.T) {
	var upstreamBody map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Errorf("body decode: %v", err)
		}
		assertOpenCodeHeaders(t, r)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "chat"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],"tool_choice":"required",`+
			`"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := upstreamBody["tool_choice"]; got != "auto" {
		t.Errorf("upstream tool_choice = %v, want auto (the gateway rejects every other value)", got)
	}
}

func TestForwardOpenCodeStyleCallMessages(t *testing.T) {
	sanitized := openCodeSanitizedBash()
	anthropicSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"" + sanitized + "\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		assertOpenCodeHeaders(t, r)

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != len(opencode.RequiredToolNames())+1 {
			t.Errorf("upstream tools = %d, want %d (7 required + 1 renamed client tool)", len(tools), len(opencode.RequiredToolNames())+1)
		}
		seen := map[string]bool{}
		for _, entry := range tools {
			tm, _ := entry.(map[string]any)
			name, _ := tm["name"].(string)
			seen[name] = true
			if name == sanitized {
				if _, ok := tm["input_schema"].(map[string]any); !ok {
					t.Errorf("client tool %q lost input_schema: %v", name, tm)
				}
			}
		}
		for _, name := range opencode.RequiredToolNames() {
			if !seen[name] {
				t.Errorf("required tool %q missing", name)
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, anthropicSSE)
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "messages"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}],`+
			`"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"name":"bash"`) {
		t.Fatalf("client must see the original tool name:\n%s", body)
	}
	if strings.Contains(body, sanitized) {
		t.Fatalf("client must not see the renamed tool:\n%s", body)
	}
}

func TestForwardOpenCodeClampsMinTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if got, _ := body["max_output_tokens"].(float64); got < 16 {
			t.Errorf("upstream max_output_tokens = %v, want >= 16", body["max_output_tokens"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistryWithStyleCall(upstream, "responses"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardOpenCodeDefaultsToChat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeTokenExchange(w, r)
		if strings.HasPrefix(r.URL.Path, "/auth/device/token") {
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions (default dialect)", r.URL.Path)
		}
		assertOpenCodeHeaders(t, r)

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := newOpencodeTestProxy(t, upstream, opencodeTestRegistry(upstream))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("content missing:\n%s", w.Body.String())
	}
}
