package config

import (
	"os/exec"
	"strings"
	"testing"

	"routerllm/internal/relay"
)

func TestApplyOpencodeRelayDisabledLeavesBaseURL(t *testing.T) {
	t.Setenv("OPENCODE_TLS_RELAY", "")
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "oc", Style: "opencode", BaseURL: "https://opencode.ai/inference/openai"},
	}}

	applyOpencodeRelay(cfg)

	if cfg.Providers[0].BaseURL != "https://opencode.ai/inference/openai" {
		t.Fatalf("base_url = %q, want untouched when the relay is disabled", cfg.Providers[0].BaseURL)
	}
}

func TestApplyOpencodeRelayFallbackWhenBinMissing(t *testing.T) {
	t.Setenv("OPENCODE_TLS_RELAY", "1")
	t.Setenv("OPENCODE_RELAY_BIN", "/nonexistent/bun")
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "oc", Style: "opencode", BaseURL: "https://opencode.ai/inference/openai"},
	}}

	applyOpencodeRelay(cfg)

	if cfg.Providers[0].BaseURL != "https://opencode.ai/inference/openai" {
		t.Fatalf("base_url = %q, want the original after relay spawn failure (fallback direct)", cfg.Providers[0].BaseURL)
	}
}

func TestApplyOpencodeRelaySkipsOtherStyles(t *testing.T) {
	t.Setenv("OPENCODE_TLS_RELAY", "1")
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "openai-style", Style: "openai", BaseURL: "https://api.example.com/v1"},
	}}

	applyOpencodeRelay(cfg)

	if cfg.Providers[0].BaseURL != "https://api.example.com/v1" {
		t.Fatalf("base_url = %q, want untouched for a non-opencode style", cfg.Providers[0].BaseURL)
	}
}

func TestApplyOpencodeRelaySkipsDisabled(t *testing.T) {
	t.Setenv("OPENCODE_TLS_RELAY", "1")
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "oc-off", Style: "opencode", BaseURL: "https://opencode.ai/inference/openai", Disabled: true},
	}}

	applyOpencodeRelay(cfg)

	if cfg.Providers[0].BaseURL != "https://opencode.ai/inference/openai" {
		t.Fatalf("base_url = %q, want untouched for a disabled provider", cfg.Providers[0].BaseURL)
	}
}

func TestApplyOpencodeRelayRewritesAndKeepsOrigin(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not on PATH — spawn tests need it")
	}
	t.Setenv("OPENCODE_TLS_RELAY", "1")
	t.Setenv("OPENCODE_RELAY_PORT", "0")
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "oc", Style: "opencode", BaseURL: "https://opencode.ai/inference/openai"},
	}}
	t.Cleanup(relay.Default().Stop)

	applyOpencodeRelay(cfg)

	base := cfg.Providers[0].BaseURL
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("base_url = %q, want the local relay URL", base)
	}
	// Second call must be idempotent against the same relay child.
	applyOpencodeRelay(cfg)
	if cfg.Providers[0].BaseURL != base {
		t.Fatalf("base_url = %q, want the same relay URL on reload", cfg.Providers[0].BaseURL)
	}
}
