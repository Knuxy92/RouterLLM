package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routerllm/internal/codex"
	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

// codexChatSSE is a typical Codex Responses event sequence: reasoning summary,
// two text deltas, then the terminal event carrying usage.
const codexChatSSE = "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"codex\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"

// codexToolCallSSE streams one function call whose arguments arrive in two
// fragments; the argument events identify the call by item id, not call id.
const codexToolCallSSE = "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_9\",\"name\":\"get_weather\"}}\n\n" +
	"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"city\\\":\"}\n\n" +
	"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"\\\"Paris\\\"}\"}\n\n" +
	"data: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":5}}}\n\n"

func codexRegistry(baseURL string, refreshTokens ...string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name: "codex", BaseURL: baseURL, Style: "codex", Keys: refreshTokens,
	}}, []model.Rule{{
		ModelID: "codex-test", Routes: []model.Spec{{Provider: "codex", Model: "gpt-5.6-codex"}},
	}}, time.Minute)
}

func codexRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
}

// newCodexProxy builds the proxy and injects a manager whose token endpoint is
// the fake upstream, so no request ever reaches the real auth host.
func newCodexProxy(t *testing.T, upstream *httptest.Server, refreshTokens ...string) *Proxy {
	t.Helper()
	t.Setenv("CODEX_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "codex-accounts.json"))

	proxy := NewProxy(codexRegistry(upstream.URL, refreshTokens...), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.codexMu.Lock()
	proxy.codexManager = codex.NewManager(&codex.Client{
		HTTPClient: upstream.Client(),
		Endpoints:  codex.Endpoints{Token: upstream.URL + "/oauth/token"},
	}, nil)
	proxy.codexMu.Unlock()

	return proxy
}

// codexTestToken builds an unsigned JWT carrying the claims the provider
// fingerprints; the router only decodes, it never verifies.
func codexTestToken(seq int) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"exp":                            time.Now().Add(time.Hour).Unix(),
		"jti":                            fmt.Sprintf("tok-%d", seq),
		"https://api.openai.com/auth":    map[string]any{"chatgpt_account_id": "acct_1"},
		"https://api.openai.com/profile": map[string]any{"email": "test@example.test"},
	})

	return header + "." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// codexTokenHandler serves the OAuth refresh endpoint: it asserts the form the
// manager must send and rotates both tokens on every call.
func codexTokenHandler(t *testing.T, refreshes *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") == "" || r.PostForm.Get("client_id") == "" {
			t.Errorf("token form = %v", r.PostForm)
		}

		(*refreshes)++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  codexTestToken(*refreshes),
			"refresh_token": fmt.Sprintf("rot-%d", *refreshes),
			"expires_in":    3600,
		})
	}
}

// codexStreamChunks decodes the OpenAI chunks a client received.
func codexStreamChunks(t *testing.T, body string) []model.StreamChunk {
	t.Helper()

	var chunks []model.StreamChunk
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var chunk model.StreamChunk
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %v\n%s", err, line)
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

func TestForwardCodexStreamsTranslatedSSE(t *testing.T) {
	refreshes := 0
	var gotBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, &refreshes))
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("authorization = %q, want a bearer token", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct_1" {
			t.Errorf("chatgpt account id = %q, want acct_1", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode codex request: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexChatSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newCodexProxy(t, upstream, "refresh-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"codex-test","stream":true,"reasoning_effort":"high","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if gotBody["model"] != "gpt-5.6-codex" {
		t.Errorf("upstream model = %v", gotBody["model"])
	}
	if gotBody["instructions"] != "be brief" {
		t.Errorf("instructions = %v, want the system message", gotBody["instructions"])
	}
	if gotBody["stream"] != true || gotBody["store"] != false {
		t.Errorf("stream = %v, store = %v, want true and false", gotBody["stream"], gotBody["store"])
	}

	input, ok := gotBody["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one user item", gotBody["input"])
	}
	user, _ := input[0].(map[string]any)
	if user["role"] != "user" || user["content"] != "hi" {
		t.Errorf("input[0] = %#v", input[0])
	}

	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want high/auto", gotBody["reasoning"])
	}

	body := w.Body.String()
	if !strings.Contains(body, `"object":"chat.completion.chunk"`) {
		t.Fatalf("body is not a chunk stream: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream does not end with [DONE]: %s", body)
	}

	var content, reasoningText strings.Builder
	var finish string
	for _, chunk := range codexStreamChunks(t, body) {
		if chunk.Model != "gpt-5.6-codex" {
			t.Errorf("chunk model = %q", chunk.Model)
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			reasoningText.WriteString(choice.Delta.ReasoningContent)
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
	}

	if content.String() != "hello codex" {
		t.Errorf("streamed content = %q, want hello codex", content.String())
	}
	if reasoningText.String() != "thinking" {
		t.Errorf("streamed reasoning = %q, want thinking", reasoningText.String())
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
	if refreshes < 1 {
		t.Errorf("refreshes = %d, want at least one token exchange", refreshes)
	}
}

func TestForwardCodexNonStreamingBuffers(t *testing.T) {
	refreshes := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, &refreshes))
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexChatSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newCodexProxy(t, upstream, "refresh-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"codex-test","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("client body is not a single JSON response: %v\n%s", err, w.Body.String())
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if got := resp.Choices[0].Message.Content; got != "hello codex" {
		t.Errorf("content = %q, want hello codex", got)
	}
	if got := resp.Choices[0].Message.ReasoningContent; got != "thinking" {
		t.Errorf("reasoning_content = %q, want thinking", got)
	}
	if got := resp.Choices[0].FinishReason; got != "stop" {
		t.Errorf("finish_reason = %q, want stop", got)
	}
	if !strings.Contains(string(resp.Usage), `"completion_tokens":7`) {
		t.Errorf("usage = %s, want the upstream completion token count", resp.Usage)
	}
}

func TestForwardRawRefreshesCodexTokenOnUnauthorized(t *testing.T) {
	refreshes := 0
	attempts := 0
	var authHeaders []string

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, &refreshes))
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"token_expired","message":"expired"}}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newCodexProxy(t, upstream, "refresh-1")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", codexRequest(), map[string]any{
		"model":    "codex-test",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if refreshes < 1 {
		t.Fatalf("refreshes = %d, want at least 1", refreshes)
	}
	if len(authHeaders) != 2 || authHeaders[1] == authHeaders[0] {
		t.Fatalf("authorization headers = %#v, want a refreshed token on retry", authHeaders)
	}
	if !strings.HasPrefix(authHeaders[1], "Bearer ") {
		t.Fatalf("retry authorization = %q, want a bearer token", authHeaders[1])
	}
}

func TestForwardCodexTranslationBuildsToolCall(t *testing.T) {
	refreshes := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, &refreshes))
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexToolCallSSE)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	proxy := newCodexProxy(t, upstream, "refresh-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"codex-test","stream":true,"messages":[{"role":"user","content":"weather?"}],`+
			`"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("stream does not end with [DONE]: %s", w.Body.String())
	}

	calls := map[int]*model.ToolCall{}
	var finish string
	for _, chunk := range codexStreamChunks(t, w.Body.String()) {
		for _, choice := range chunk.Choices {
			for _, fragment := range choice.Delta.ToolCalls {
				mergeToolCallFragment(calls, fragment)
			}
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
	}

	assembled := assembleToolCalls(calls)
	if len(assembled) != 1 {
		t.Fatalf("tool_calls = %+v, want 1 assembled call", assembled)
	}
	if assembled[0].ID != "call_9" || assembled[0].Type != "function" || assembled[0].Function.Name != "get_weather" {
		t.Fatalf("tool_call identity = %+v", assembled[0])
	}
	if assembled[0].Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("arguments = %q, want the concatenated fragments", assembled[0].Function.Arguments)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", finish)
	}
}
