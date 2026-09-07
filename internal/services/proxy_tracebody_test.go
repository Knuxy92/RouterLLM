package services

import (
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
	"routerllm/internal/telemetry"
)

// newTraceProxy spins a proxy over one upstream whose replies are scripted,
// plus a telemetry store to inspect recorded events.
func newTraceProxy(t *testing.T, upstreamHandler http.HandlerFunc) (*Proxy, *telemetry.Store) {
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
		Style:   "openai",
		Keys:    []string{"key-1"},
	}}, []model.Rule{{
		ModelID: "m",
		Routes:  []model.Spec{{Provider: "prov", Model: "up"}},
	}}, time.Minute)

	p := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	p.SetTelemetry(store)

	return p, store
}

func traceRequest(t *testing.T, p *Proxy) {
	t.Helper()

	body := strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	p.Forward("/v1/chat/completions", httptest.NewRecorder(), req)
}

func TestErrorEventCapturesUpstreamBody(t *testing.T) {
	p, store := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"message":"credit balance too low","code":402}}`)
	})
	traceRequest(t, p)

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", e.Status)
	}
	if !strings.Contains(e.RespBody, "credit balance too low") {
		t.Fatalf("event resp_body missing upstream message: %q", e.RespBody)
	}
	if len(e.Attempts) != 1 || !strings.Contains(e.Attempts[0].RespBody, "credit balance too low") {
		t.Fatalf("attempt resp_body missing upstream message: %+v", e.Attempts)
	}
}

func TestErrorEventCapturesBodyOnAllKeysExhausted(t *testing.T) {
	// deadStatuses (401) kills the only key → "all keys exhausted" path.
	p, store := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid api key","type":"auth_error"}}`)
	})
	traceRequest(t, p)

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if !strings.Contains(e.Err, "all keys exhausted") {
		t.Fatalf("err = %q, want exhausted", e.Err)
	}
	if !strings.Contains(e.RespBody, "invalid api key") {
		t.Fatalf("event resp_body missing upstream body: %q", e.RespBody)
	}
}

func TestSuccessEventHasNoBody(t *testing.T) {
	p, store := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	})
	traceRequest(t, p)

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].RespBody != "" {
		t.Fatalf("successful request captured a body: %q", events[0].RespBody)
	}
}

func TestClampBodyTruncates(t *testing.T) {
	got := telemetry.ClampBody([]byte(strings.Repeat("x", 5000)), telemetry.RespBodyCap)
	if len(got) > telemetry.RespBodyCap+16 || !strings.HasSuffix(got, "…(truncated)") {
		t.Fatalf("ClampBody did not truncate: len=%d suffix=%q", len(got), got[len(got)-20:])
	}
	if telemetry.ClampBody(nil, telemetry.RespBodyCap) != "" {
		t.Fatal("empty body should stay empty")
	}
}
