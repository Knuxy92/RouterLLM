package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"routerllm/internal/adapter"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "1767"
	}
	targetURL := os.Getenv("TARGET_URL")
	apiKey := os.Getenv("TARGET_API_KEY")

	if targetURL == "" {
		log.Printf("TARGET_URL not set — running in capture-only mode")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		fmt.Printf("\n%s — IN (Anthropic)\n", time.Now().Format(time.RFC3339))
		fmt.Println(string(raw))
		fmt.Println()

		if targetURL == "" {
			sendAnthropicResponse(w, raw)
			return
		}

		translated, err := adapter.AnthropicRequestToOpenAI(raw)
		if err != nil {
			http.Error(w, "translation error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		translated = withStream(translated, true)

		fmt.Printf("--- OUT (OpenAI request, stream=true) ---\n%s\n", string(translated))

		resp, err := doUpstream(targetURL, apiKey, translated)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			log.Printf("upstream error (status=%d): %s", resp.StatusCode, brief(raw))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(raw)
			return
		}

		// Always stream Anthropic SSE to client
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		adapter.StreamOpenAIToAnthropicSSE(resp.Body, w, extractModel(raw))
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("listening on :%s target=%s", port, targetURL)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func doUpstream(targetURL, apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return http.DefaultClient.Do(req)
}

func sendAnthropicResponse(w http.ResponseWriter, raw []byte) {
	var body map[string]any
	json.Unmarshal(raw, &body)
	model := extractString(body, "model")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_captured", "type": "message", "role": "assistant",
			"content": []any{}, "model": model,
		},
	})
	emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	emit("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]string{"type": "text_delta", "text": "Captured — check server logs."},
	})
	emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": 0,
	})
	emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
	})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func extractString(body map[string]any, key string) string {
	v, _ := body[key].(string)
	return v
}

func extractModel(raw []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &v)
	return v.Model
}

func withStream(body []byte, val bool) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["stream"] = val
	out, _ := json.Marshal(m)
	return out
}

func brief(data []byte) string {
	s := string(data)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
