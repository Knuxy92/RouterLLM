package services

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/provider"
	"routerllm/internal/telemetry"
)

// newTestRunnerProxy spins a proxy over one upstream whose replies are
// scripted, plus a telemetry store to inspect recorded events. RunTest
// bypasses routes, so the registry needs no rules.
func newTestRunnerProxy(t *testing.T, style string, upstreamHandler http.HandlerFunc) (*Proxy, *telemetry.Store) {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	store, err := telemetry.NewStore("")
	if err != nil {
		t.Fatal(err)
	}

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "prov",
		BaseURL: upstream.URL,
		Style:   style,
		Keys:    []string{"k1"},
	}}, nil, time.Minute)

	p := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	p.SetTelemetry(store)

	return p, store
}

func TestRunTestOpenAIStream(t *testing.T) {
	p, store := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"po\"}}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ng\"}}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":7,\"total_tokens\":8}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", MaxTokens: 16})

	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if res.Style != "openai" {
		t.Fatalf("style = %q, want openai", res.Style)
	}
	if res.Content != "pong" {
		t.Fatalf("content = %q, want pong", res.Content)
	}
	if res.Tokens <= 0 {
		t.Fatalf("tokens = %d, want > 0", res.Tokens)
	}
	if res.TTFTMS < 0 {
		t.Fatalf("ttft_ms = %d, want >= 0", res.TTFTMS)
	}

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Status != http.StatusOK || e.Provider != "prov" || e.UpstreamModel != "up-test" {
		t.Fatalf("event = %+v", e)
	}
	if e.TokensOut != res.Tokens {
		t.Fatalf("event tokens_out = %d, want %d", e.TokensOut, res.Tokens)
	}
	if e.RespBody != "" {
		t.Fatalf("successful run captured a body: %q", e.RespBody)
	}
}

func TestRunTestUpstreamError(t *testing.T) {
	p, store := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"upstream exploded"}}`)
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", MaxTokens: 16})

	if res.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Status)
	}
	if !strings.Contains(res.Error, "upstream exploded") {
		t.Fatalf("error = %q, want upstream body", res.Error)
	}

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].RespBody, "upstream exploded") {
		t.Fatalf("event resp_body = %q", events[0].RespBody)
	}
}

func TestRunTestTimeout(t *testing.T) {
	p, _ := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", Timeout: 300 * time.Millisecond})

	if !strings.Contains(res.Error, "timed out") {
		t.Fatalf("error = %q, want timed out", res.Error)
	}
	if res.Status != 0 {
		t.Fatalf("status = %d, want 0", res.Status)
	}
}

func TestRunTestAnthropicTranslatesPath(t *testing.T) {
	gotPath := make(chan string, 1)
	p, store := newTestRunnerProxy(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-x\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":3}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", MaxTokens: 16})

	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if res.Content != "hello" {
		t.Fatalf("content = %q, want hello", res.Content)
	}
	if res.Tokens != 3 {
		t.Fatalf("tokens = %d, want 3", res.Tokens)
	}
	if path := <-gotPath; path != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages", path)
	}

	events := store.Since(0)
	if len(events) != 1 || events[0].Status != http.StatusOK {
		t.Fatalf("events = %+v", events)
	}
}

func TestRunTestProviderNotFound(t *testing.T) {
	p, store := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {})

	res := p.RunTest(context.Background(), TestRequest{Provider: "nope", Model: "m", Prompt: "ping"})

	if res.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Status)
	}
	if res.Error == "" {
		t.Fatal("error is empty, want provider not found message")
	}
	if events := store.Since(0); len(events) != 0 {
		t.Fatalf("telemetry events = %d, want 0 (no upstream attempt)", len(events))
	}
}
