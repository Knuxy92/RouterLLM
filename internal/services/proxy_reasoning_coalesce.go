package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// reasoningCoalescer merges the per-token reasoning_content deltas the
// WorkBuddy upstream streams (one ~1-token chunk each, DeepSeek-style) into
// one chunk per reasoning segment. OpenRouter-style upstreams deliver the
// whole reasoning trace as a single delta, and clients that open a fresh
// think block per chunk render the WorkBuddy stream as hundreds of one-token
// blocks — so the workbuddy dialect buffers reasoning-only chunks and emits
// the accumulated text just before the next content/tool/finish frame (and
// before [DONE], plus on stream end via flushPending).
type reasoningCoalescer struct {
	http.ResponseWriter

	id      string
	model   string
	created any
	pending strings.Builder
}

func newReasoningCoalescer(w http.ResponseWriter) *reasoningCoalescer {
	return &reasoningCoalescer{ResponseWriter: w}
}

// sseFrame is the slice of a chat.completion.chunk the coalescer needs.
type sseFrame struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created any    `json:"created"`
	Choices []struct {
		Index        int `json:"index"`
		FinishReason any `json:"finish_reason"`
		Delta        struct {
			Content         any    `json:"content"`
			Reasoning       string `json:"reasoning_content"`
			ToolCalls       []any  `json:"tool_calls"`
			ToolCallsNumber any    `json:"function_call"`
		} `json:"delta"`
	} `json:"choices"`
}

func (c *reasoningCoalescer) Write(p []byte) (int, error) {
	payload, ok := ssePayload(p)
	if !ok {
		return c.passthrough(p)
	}
	if payload == "[DONE]" {
		c.flushPending()

		return c.passthrough(p)
	}

	var frame sseFrame
	if err := json.Unmarshal([]byte(payload), &frame); err != nil || len(frame.Choices) != 1 {
		// Error frames carry no choices; anything unparsable goes through.
		return c.passthrough(p)
	}

	choice := frame.Choices[0]
	contentOnly := choice.Delta.Content == nil || choice.Delta.Content == ""
	// The upstream sends "finish_reason": "" on non-final chunks — only a
	// non-empty value (or null-with-later-text) marks the frame final.
	finishSet := choice.FinishReason != nil && choice.FinishReason != ""
	pureReasoning := choice.Delta.Reasoning != "" && contentOnly &&
		len(choice.Delta.ToolCalls) == 0 && choice.Delta.ToolCallsNumber == nil &&
		!finishSet
	if !pureReasoning {
		c.flushPending()

		return c.passthrough(p)
	}

	if c.pending.Len() == 0 {
		c.id, c.model, c.created = frame.ID, frame.Model, frame.Created
	}
	c.pending.WriteString(choice.Delta.Reasoning)

	return len(p), nil
}

// ssePayload extracts the payload between the SSE wrapper: only plain
// "data: <payload>" frames are recognized; anything else is not ours to hold.
func ssePayload(p []byte) (string, bool) {
	s := string(p)
	if !strings.HasPrefix(s, "data: ") || !strings.HasSuffix(s, "\n\n") {
		return "", false
	}

	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")), true
}

func (c *reasoningCoalescer) passthrough(p []byte) (int, error) {
	return c.ResponseWriter.Write(p)
}

// flushPending emits the buffered reasoning as one chunk in the shape of the
// first buffered frame, then resets the buffer.
func (c *reasoningCoalescer) flushPending() {
	if c.pending.Len() == 0 {
		return
	}
	text := c.pending.String()
	c.pending.Reset()

	created := c.created
	if created == nil {
		created = 0
	}
	chunk, err := json.Marshal(map[string]any{
		"id":      c.id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   c.model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{"reasoning_content": text},
			"finish_reason": nil,
		}},
	})
	if err != nil {
		return
	}

	_, _ = fmt.Fprintf(c.ResponseWriter, "data: %s\n\n", chunk)
	if flusher, ok := c.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Flush satisfies http.Flusher for the SSE writer chain without releasing the
// buffered reasoning — the pending text is only emitted by flushPending, so
// the per-frame flushes around it cannot fragment it again.
func (c *reasoningCoalescer) Flush() {
	if flusher, ok := c.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
