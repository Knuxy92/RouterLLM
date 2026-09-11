package services

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestReadErrorBodyCapsAt64KiB(t *testing.T) {
	body := ReadErrorBody(bytes.NewReader(make([]byte, 1<<20)))

	if len(body) != maxErrorBodyRead {
		t.Fatalf("read %d bytes, want the %d-byte cap", len(body), maxErrorBodyRead)
	}
}

func TestTruncateErrorBodyCapsAt16KiB(t *testing.T) {
	short := []byte("small")
	if got := TruncateErrorBody(short); string(got) != "small" {
		t.Fatalf("short body = %q", got)
	}

	long := bytes.Repeat([]byte("x"), maxClientErrorBytes+100)
	if got := TruncateErrorBody(long); len(got) != maxClientErrorBytes {
		t.Fatalf("long body = %d bytes, want %d", len(got), maxClientErrorBytes)
	}
}

// TestUpstreamErrorBodyEchoIsBounded serves a 1 MiB error body from a fake
// upstream and checks the client-visible error payload stays within the
// 16 KiB echo cap.
func TestUpstreamErrorBodyEchoIsBounded(t *testing.T) {
	huge := strings.Repeat("x", 1<<20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, huge)
	}))
	defer upstream.Close()

	proxy := NewProxy(newHeaderTestRegistry(upstream), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("client error payload is not JSON: %v", err)
	}
	if parsed.Error.Message == "" {
		t.Fatal("client error message is empty")
	}
	if len(parsed.Error.Message) > maxClientErrorBytes {
		t.Fatalf("client error message = %d bytes, want <= %d", len(parsed.Error.Message), maxClientErrorBytes)
	}
	if w.Body.Len() > maxClientErrorBytes+512 {
		t.Fatalf("client error payload = %d bytes, want bounded", w.Body.Len())
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// TestTransportErrorRedactsProviderQuery checks that a provider's query string
// (which can carry credentials) never lands in the client error, the log line,
// or the telemetry event on a transport failure.
func TestTransportErrorRedactsProviderQuery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "test",
		BaseURL: upstream.URL,
		Style:   "openai",
		Keys:    []string{"provider-key"},
		Query:   "?api-version=2024&secret=xyz",
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "test", Model: "upstream-model"}},
	}}, time.Minute)

	var logBuf strings.Builder
	proxy := NewProxy(registry, &http.Client{Transport: failingTransport{}}, log.New(&logBuf, "", 0), false, false, false, false, nil, "")

	store, err := telemetry.NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	proxy.SetTelemetry(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if strings.Contains(w.Body.String(), "xyz") {
		t.Fatalf("client error leaked the provider query: %s", w.Body.String())
	}
	if strings.Contains(logBuf.String(), "xyz") {
		t.Fatalf("log leaked the provider query:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "?…redacted") {
		t.Fatalf("log line does not show the redacted query marker:\n%s", logBuf.String())
	}

	events := store.Since(0)
	if len(events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(events))
	}
	if strings.Contains(events[0].Err, "xyz") || strings.Contains(events[0].RespBody, "xyz") {
		t.Fatalf("telemetry leaked the provider query: err=%q body=%q", events[0].Err, events[0].RespBody)
	}
}
