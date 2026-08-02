package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"routerllm/internal/config"
)

func TestResolveAutoModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("unexpected classifier request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Coding Model"}}]}`)
	}))
	defer server.Close()

	model, err := resolveAutoModel(context.Background(), server.Client(), config.AutoModelConfig{
		Enabled:       true,
		BaseURL:       server.URL,
		APIKey:        "key",
		Model:         "router-model",
		Prompt:        "classify",
		SmallModel:    "small",
		AnalysisModel: "analysis",
		CodingModel:   "coding",
	}, map[string]any{"messages": []any{map[string]any{"role": "user", "content": "write code"}}})
	if err != nil {
		t.Fatal(err)
	}
	if model != "coding" {
		t.Fatalf("model = %q, want coding", model)
	}
}

func TestResolveAutoModelDisabled(t *testing.T) {
	_, err := resolveAutoModel(nil, nil, config.AutoModelConfig{}, nil)
	if err != ErrAutoModelDisabled {
		t.Fatalf("error = %v, want %v", err, ErrAutoModelDisabled)
	}

}

func TestResolveAutoModelUsesResponsesInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "answer this") {
			t.Fatalf("classifier request does not contain responses input: %s", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Small Model"}}]}`)
	}))
	defer server.Close()

	model, err := resolveAutoModel(context.Background(), server.Client(), config.AutoModelConfig{
		Enabled:    true,
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "router-model",
		Prompt:     "classify",
		SmallModel: "small",
	}, map[string]any{"input": "answer this"})
	if err != nil {
		t.Fatal(err)
	}
	if model != "small" {
		t.Fatalf("model = %q, want small", model)
	}
}

func TestResolveAutoModelRetriesInvalidCategory(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}

		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Here is the answer"}}]}`)
			return
		}
		if !strings.Contains(string(body), `previous output \"Here is the answer\" was invalid`) {
			t.Fatalf("retry does not contain corrective prompt: %s", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Coding Model"}}]}`)
	}))
	defer server.Close()

	model, err := resolveAutoModel(context.Background(), server.Client(), config.AutoModelConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		APIKey:      "key",
		Model:       "router-model",
		Prompt:      "classify",
		CodingModel: "coding",
	}, map[string]any{"messages": []any{"write code"}})
	if err != nil {
		t.Fatal(err)
	}
	if model != "coding" || calls.Load() != 2 {
		t.Fatalf("model = %q, calls = %d", model, calls.Load())
	}
}

func TestProxyResolveAutoModelCachesByChatID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Small Model"}}]}`)
	}))
	defer server.Close()

	proxy := NewProxy(nil, server.Client(), nil, false, false, false, "", config.AutoModelConfig{
		Enabled:    true,
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "router-model",
		SmallModel: "small",
	})
	body := map[string]any{"model": "auto", "messages": []any{"first"}}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Chat-ID", "chat-1")

	first, cached := proxy.resolveAutoModel(r.Context(), body, chatIdentifier(r, body))
	if first.err != nil || cached || first.model != "small" {
		t.Fatalf("first resolution = %#v, cached=%v", first, cached)
	}

	body["messages"] = []any{"second"}
	second, cached := proxy.resolveAutoModel(r.Context(), body, chatIdentifier(r, body))
	if second.err != nil || !cached || second.model != "small" {
		t.Fatalf("second resolution = %#v, cached=%v", second, cached)
	}
	if calls.Load() != 1 {
		t.Fatalf("classifier calls = %d, want 1", calls.Load())
	}
}

func TestProxyResolveAutoModelDoesNotCacheWithoutChatID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Small Model"}}]}`)
	}))
	defer server.Close()

	proxy := NewProxy(nil, server.Client(), nil, false, false, false, "", config.AutoModelConfig{
		Enabled:    true,
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "router-model",
		SmallModel: "small",
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	for i := 0; i < 2; i++ {
		result, cached := proxy.resolveAutoModel(r.Context(), map[string]any{"messages": []any{fmt.Sprint(i)}}, "")
		if result.err != nil || cached {
			t.Fatalf("resolution %d = %#v, cached=%v", i, result, cached)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("classifier calls = %d, want 2", calls.Load())
	}
}

func TestChatIdentifier(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Conversation-ID", "conversation-1")
	if got := chatIdentifier(r, map[string]any{"metadata": map[string]any{"chat_id": "body-1"}}); got != "conversation-1" {
		t.Fatalf("header chat id = %q", got)
	}

	r.Header.Del("X-Conversation-ID")
	if got := chatIdentifier(r, map[string]any{"metadata": map[string]any{"chat_id": "body-1"}}); got != "body-1" {
		t.Fatalf("metadata chat id = %q", got)
	}
}
