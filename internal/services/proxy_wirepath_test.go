package services

import (
	"testing"

	"routerllm/internal/provider"
)

func TestWirePathStripsV1ForOpaqueStyles(t *testing.T) {
	for _, style := range []string{"openai", "anthropic"} {
		pv := &provider.Provider{Style: style}

		for inbound, want := range map[string]string{
			"/v1/chat/completions": "/chat/completions",
			"/v1/responses":        "/responses",
			"/v1/messages":         "/messages",
		} {
			if got := wirePath(pv, inbound); got != want {
				t.Errorf("wirePath(%s, %q) = %q, want %q", style, inbound, got, want)
			}
		}
	}
}

func TestWirePathKeepsVerbatimForOtherStyles(t *testing.T) {
	for _, style := range []string{"google", "cline", "codex", "opencode", "alysis"} {
		pv := &provider.Provider{Style: style}

		for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
			if got := wirePath(pv, path); got != path {
				t.Errorf("wirePath(%s, %q) = %q, want verbatim", style, path, got)
			}
		}
	}
}

func TestWireFilesPath(t *testing.T) {
	if got := wireFilesPath(&provider.Provider{Style: "openai"}); got != "/files" {
		t.Errorf("wireFilesPath(openai) = %q, want /files", got)
	}
	if got := wireFilesPath(&provider.Provider{Style: "anthropic"}); got != "/files" {
		t.Errorf("wireFilesPath(anthropic) = %q, want /files", got)
	}

	for _, style := range []string{"google", "cline", "codex", "opencode", "alysis"} {
		if got := wireFilesPath(&provider.Provider{Style: style}); got != "/v1/files" {
			t.Errorf("wireFilesPath(%s) = %q, want /v1/files", style, got)
		}
	}
}
