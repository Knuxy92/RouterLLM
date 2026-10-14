package services

import (
	"io"
	"log"
	"net/http"

	"routerllm/internal/adapter"
	"routerllm/internal/util"
)

func writeStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// serveOpenAI serves an openai-dialect leg to the client. It returns the raw
// upstream payload when the stream carried an error frame instead of content,
// so the caller can record it; the client itself only ever sees the sanitized
// error.
func (p *Proxy) serveOpenAI(resp *http.Response, clientStream bool, w http.ResponseWriter, toolNameRestore map[string]string) string {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := util.StreamSSETransform(resp.Body, w, true, streamRestoreTransform(toolNameRestore)); err != nil {
			log.Printf("stream error: %v", err)
		}
		return ""
	}

	result, upstreamErr := bufferStream(resp.Body)
	if upstreamErr != "" {
		p.log.Printf("upstream answered 200 but streamed an error frame: %s", briefBody([]byte(upstreamErr)))
		util.WriteError(w, http.StatusBadGateway, "upstream_error", "the upstream returned an error")

		return upstreamErr
	}

	util.WriteJSON(w, http.StatusOK, restoreBufferedToolNames(result, toolNameRestore))

	return ""
}

func serveResponses(resp *http.Response, clientStream bool, w http.ResponseWriter, toolNameRestore map[string]string) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := util.StreamSSETransform(resp.Body, w, false, responsesRestoreTransform(toolNameRestore)); err != nil {
			log.Printf("responses stream error: %v", err)
		}
		return
	}

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(restoreResponsesPayloadNames(string(body), toolNameRestore)))
}

// responsesRestoreTransform adapts the Responses-shape name restorer to the SSE
// frame hook.
func responsesRestoreTransform(reverse map[string]string) func(string) string {
	if len(reverse) == 0 {
		return nil
	}

	return func(payload string) string {
		return restoreResponsesPayloadNames(payload, reverse)
	}
}

func (p *Proxy) serveAnthropic(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter, toolNameRestore map[string]string) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamAnthropicToOpenAI(resp.Body, newRestoringWriter(w, toolNameRestore), modelName); err != nil {
			p.log.Printf("anthropic stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferAnthropicToOpenAI(resp.Body, modelName)
	if err != nil {
		p.log.Printf("failed to translate anthropic response: %v", err)
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response")
		return
	}

	util.WriteJSON(w, http.StatusOK, restoreToolCallNamesJSON(openaiBody, toolNameRestore))
}

func (p *Proxy) serveGoogle(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamGoogleToOpenAI(resp.Body, w, modelName); err != nil {
			p.log.Printf("google stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferGoogleToOpenAI(resp.Body, modelName)
	if err != nil {
		p.log.Printf("failed to translate google response: %v", err)
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(openaiBody)
}

// serveResponsesAsChat serves a Responses-dialect leg to a chat-completions
// client: the upstream Responses SSE is converted back into chat chunks or
// buffered into a single chat.completion document.
func (p *Proxy) serveResponsesAsChat(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter, toolNameRestore map[string]string) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamResponsesToOpenAI(resp.Body, newRestoringWriter(w, toolNameRestore), modelName); err != nil {
			p.log.Printf("responses stream error: %v", err)
		}
		return
	}

	data, err := adapter.BufferResponsesToOpenAI(resp.Body, modelName)
	if err != nil {
		p.log.Printf("failed to translate responses response: %v", err)
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response")
		return
	}

	restored := restoreToolCallNamesJSON(data, toolNameRestore)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(restored)
}

func (p *Proxy) serveCodex(resp *http.Response, clientStream bool, modelName string, w http.ResponseWriter) {
	defer resp.Body.Close()
	if clientStream {
		writeStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		if err := adapter.StreamCodexToOpenAI(resp.Body, w, modelName); err != nil {
			p.log.Printf("codex stream error: %v", err)
		}
		return
	}

	openaiBody, err := adapter.BufferCodexToOpenAI(resp.Body, modelName)
	if err != nil {
		p.log.Printf("failed to translate codex response: %v", err)
		util.WriteError(w, http.StatusBadGateway, "translation_error", "failed to translate response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(openaiBody)
}

func (p *Proxy) serveForceStream(resp *http.Response, modelName, dialect string, w http.ResponseWriter, toolNameRestore map[string]string) {
	writeStreamHeaders(w)
	w.WriteHeader(http.StatusOK)

	if dialect == "messages" {
		if err := util.StreamRawSSE(resp.Body, newRestoringWriter(w, toolNameRestore)); err != nil {
			p.log.Printf("anthropic passthrough stream error: %v", err)
		}
		return
	}

	if dialect == "google" {
		if err := adapter.StreamGoogleToAnthropicSSE(resp.Body, w, modelName); err != nil {
			p.log.Printf("google stream error: %v", err)
		}
		return
	}

	if dialect == "responses" {
		if err := adapter.StreamResponsesToAnthropicSSE(resp.Body, newRestoringWriter(w, toolNameRestore), modelName); err != nil {
			p.log.Printf("responses stream error: %v", err)
		}
		return
	}

	if dialect == "codex" {
		adapter.StreamCodexToAnthropicSSE(resp.Body, w, modelName)
		return
	}

	if err := adapter.StreamOpenAIToAnthropicSSE(resp.Body, newRestoringWriter(w, toolNameRestore), modelName); err != nil {
		p.log.Printf("openai stream error: %v", err)
	}
}
