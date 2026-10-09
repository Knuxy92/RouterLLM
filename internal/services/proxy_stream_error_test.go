package services

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// upstreamErrorFrame is what a gateway replies with when it answers 200 but
// the model call behind it failed: one SSE frame carrying the error, then DONE.
const upstreamErrorFrame = "data: {\"error\":{\"code\":\"stream_initialization_failed\",\"message\":\"failed to invoke model 'meta/muse-spark' for org_2ue3sRj: JSON schema exceeds the maximum nesting depth of 10 levels\",\"type\":\"stream_error\"}}\n\n"

func TestBufferedUpstreamErrorFrameBecomesClientError(t *testing.T) {
	p, store := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, upstreamErrorFrame)
		io.WriteString(w, "data: [DONE]\n\n")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	p.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (an upstream error frame is not an empty completion): %s", w.Code, w.Body.String())
	}

	clientBody := w.Body.String()
	for _, leaked := range []string{"org_2ue3sRj", "meta/muse-spark", "nesting depth", "stream_initialization_failed"} {
		if strings.Contains(clientBody, leaked) {
			t.Errorf("client body leaked upstream detail %q: %s", leaked, clientBody)
		}
	}

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Status != http.StatusBadGateway {
		t.Errorf("event status = %d, want 502", events[0].Status)
	}
	if !strings.Contains(events[0].RespBody, "nesting depth") {
		t.Errorf("event resp_body missing the upstream message: %q", events[0].RespBody)
	}
}

func TestBufferedStreamWithoutErrorFrameStillCompletes(t *testing.T) {
	p, store := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	p.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("content missing: %s", w.Body.String())
	}

	events := store.Since(0)
	if len(events) != 1 || events[0].Status != http.StatusOK {
		t.Fatalf("events = %+v, want a single 200 event", events)
	}
	if events[0].RespBody != "" {
		t.Errorf("successful event resp_body = %q, want empty", events[0].RespBody)
	}
}

func TestBufferedStreamWithContentBeforeErrorFrameKeepsContent(t *testing.T) {
	p, _ := newTraceProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		io.WriteString(w, upstreamErrorFrame)
		io.WriteString(w, "data: [DONE]\n\n")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	p.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — real content must not be replaced by an error: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "partial") {
		t.Fatalf("content missing: %s", w.Body.String())
	}
}