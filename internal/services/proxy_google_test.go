package services

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

const googleUpstreamSSE = "data: {\"responseId\":\"resp-9\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi from gemini\"}],\"role\":\"model\"}}]}\n\n" +
	"data: {\"candidates\":[{\"content\":{\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":4,\"totalTokenCount\":7}}\n\n"

func newGoogleRegistry(upstream *httptest.Server, keys []string) *provider.Registry {
	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "google-test",
		BaseURL: upstream.URL,
		Style:   "google",
		Keys:    keys,
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "google-test", Model: "gemini-3.8-flash"}},
	}}, time.Minute)
}

func TestGoogleUpstreamRequestShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-3.8-flash:streamGenerateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("query = %q, want alt=sse", r.URL.RawQuery)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "gkey" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, must not be sent", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}
		if _, ok := body["model"]; ok {
			t.Errorf("body must not carry model: %v", body)
		}
		if _, ok := body["contents"]; !ok {
			t.Errorf("body missing contents: %v", body)
		}
		if _, ok := body["stream"]; ok {
			t.Errorf("stream flag must not leak into gemini payload: %v", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, googleUpstreamSSE)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"gkey"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", req, map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
		"stream":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestForwardGoogleStreamAndBuffer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, googleUpstreamSSE)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"gkey"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")

	t.Run("stream client", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
			`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
		w := httptest.NewRecorder()

		proxy.Forward("/v1/chat/completions", w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, `"content":"hi from gemini"`) {
			t.Fatalf("content delta missing:\n%s", body)
		}
		if !strings.HasSuffix(body, "data: [DONE]\n\n") {
			t.Fatalf("must end with [DONE]:\n%s", body)
		}
	})

	t.Run("non-stream client", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
			`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":false}`))
		w := httptest.NewRecorder()

		proxy.Forward("/v1/chat/completions", w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var resp model.ChatCompletionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not a single JSON response: %v\n%s", err, w.Body.String())
		}
		if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hi from gemini" {
			t.Fatalf("choices = %+v", resp.Choices)
		}
		if resp.Choices[0].FinishReason != "stop" {
			t.Fatalf("finish_reason = %q", resp.Choices[0].FinishReason)
		}
		if resp.Usage == nil || !strings.Contains(string(resp.Usage), `"completion_tokens":4`) {
			t.Fatalf("usage = %s", resp.Usage)
		}
	})
}

func TestForwardGoogleForceStreamEmitsAnthropicSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, googleUpstreamSSE)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"gkey"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, true, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Fatalf("message_start missing:\n%s", body)
	}
	if !strings.Contains(body, `"text":"hi from gemini","type":"text_delta"`) {
		t.Fatalf("text delta missing:\n%s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop missing:\n%s", body)
	}
}

func TestGoogleRetriesTransient503(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, googleUpstreamSSE)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"gkey"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d after retries: %s", w.Code, w.Body.String())
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestGoogleDeadKeyFailsOverToNextKey(t *testing.T) {
	var seen atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "dead-key" {
			seen.Add(1)
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"code":403,"message":"API key not valid.","status":"PERMISSION_DENIED"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, googleUpstreamSSE)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"dead-key", "good-key"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := seen.Load(); got != 1 {
		t.Fatalf("dead key attempts = %d, want 1", got)
	}
}

func TestGoogleUpstreamErrorIsNormalized(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`)
	}))
	defer upstream.Close()

	proxy := NewProxy(newGoogleRegistry(upstream, []string{"gkey"}), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"message":"Invalid JSON payload received."`) {
		t.Fatalf("google error not normalized: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "INVALID_ARGUMENT") {
		t.Fatalf("raw google error leaked: %s", w.Body.String())
	}
}
