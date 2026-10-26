package services

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func coalescerFrames(t *testing.T, w *reasoningCoalescer) []string {
	t.Helper()

	rec := httptest.NewRecorder()
	w.ResponseWriter = rec
	for _, frame := range []string{
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":""},"finish_reason":""}]}`,
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"content":"","reasoning_content":"The"},"finish_reason":""}]}`,
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"content":"","reasoning_content":" user is"},"finish_reason":""}]}`,
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"content":"","reasoning_content":" asking 2+2."},"finish_reason":""}]}`,
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"content":"4","reasoning_content":""},"finish_reason":""}]}`,
		`{"id":"01a","model":"hy3","created":1,"choices":[{"index":0,"delta":{"content":"","reasoning_content":"","function_call":{"name":"","arguments":""}},"finish_reason":"stop"}]}`,
	} {
		if _, err := w.Write([]byte("data: " + frame + "\n\n")); err != nil {
			t.Fatal(err)
		}
	}
	w.flushPending()
	w.Write([]byte("data: [DONE]\n\n"))

	var frames []string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if payload, ok := strings.CutPrefix(line, "data: "); ok {
			frames = append(frames, payload)
		}
	}

	return frames
}

func TestReasoningCoalescerMergesTokenDeltas(t *testing.T) {
	w := newReasoningCoalescer(httptest.NewRecorder())
	frames := coalescerFrames(t, w)

	var reasoning []string
	var content []string
	for _, payload := range frames {
		if payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   any    `json:"content"`
					Reasoning string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		if r := chunk.Choices[0].Delta.Reasoning; r != "" {
			reasoning = append(reasoning, r)
		}
		if s, ok := chunk.Choices[0].Delta.Content.(string); ok && s != "" {
			content = append(content, s)
		}
	}

	if len(reasoning) != 1 {
		t.Fatalf("reasoning chunks = %d (%v), want exactly 1 merged chunk", len(reasoning), reasoning)
	}
	if reasoning[0] != "The user is asking 2+2." {
		t.Fatalf("merged reasoning = %q", reasoning[0])
	}
	if len(content) != 1 || content[0] != "4" {
		t.Fatalf("content chunks = %v, want [4]", content)
	}
}

func TestReasoningCoalescerPassthroughErrorFrames(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newReasoningCoalescer(rec)

	errFrame := `{"error":{"message":"boom"}}`
	if _, writeErr := w.Write([]byte("data: " + errFrame + "\n\n")); writeErr != nil {
		t.Fatal(writeErr)
	}
	w.flushPending()

	if !strings.Contains(rec.Body.String(), errFrame) {
		t.Fatalf("error frame must pass through untouched, got %q", rec.Body.String())
	}
}
