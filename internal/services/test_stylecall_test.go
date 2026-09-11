package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// capturedCall is what the scripted upstream reports about the request it saw.
type capturedCall struct {
	path string
	body string
}

func TestRunTestStyleCallResponses(t *testing.T) {
	got := make(chan capturedCall, 1)
	p, store := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- capturedCall{path: r.URL.Path, body: string(raw)}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"he\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"llo\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n")
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", MaxTokens: 16, StyleCall: "responses"})

	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if res.Content != "hello" {
		t.Fatalf("content = %q, want hello", res.Content)
	}

	call := <-got
	if call.path != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", call.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.body), &body); err != nil {
		t.Fatalf("upstream body is not JSON: %v; body: %s", err, call.body)
	}
	if _, ok := body["input"]; !ok {
		t.Errorf("upstream body missing \"input\": %s", call.body)
	}
	if _, ok := body["messages"]; ok {
		t.Errorf("upstream body still carries \"messages\": %s", call.body)
	}

	events := store.Since(0)
	if len(events) != 1 || events[0].Status != http.StatusOK {
		t.Fatalf("events = %+v", events)
	}
}

func TestRunTestStyleCallMessages(t *testing.T) {
	got := make(chan capturedCall, 1)
	p, _ := newTestRunnerProxy(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- capturedCall{path: r.URL.Path, body: string(raw)}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-x\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":3}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	})

	res := p.RunTest(context.Background(), TestRequest{Provider: "prov", Model: "up-test", Prompt: "ping", MaxTokens: 16, StyleCall: "messages"})

	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if res.Content != "hello" {
		t.Fatalf("content = %q, want hello", res.Content)
	}

	call := <-got
	if call.path != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages", call.path)
	}
}
