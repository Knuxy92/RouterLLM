package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAML_RoutesParsed(t *testing.T) {
	yaml := `
port: "1766"
cooldown: 60s
providers:
  - name: test-provider
    api_key: sk-test
    base_url: https://example.com
    style: openai
    auth_mode: bearer
routes:
  - model: test-model
    routes:
      - provider: test-provider
        model: gpt-4
        defaults:
          reasoning_effort: high
          thinking_budget: 8192
`
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("ROUTERLLM_CONFIG_FILE", f)
	defer os.Unsetenv("ROUTERLLM_CONFIG_FILE")

	cfg := Load()
	if cfg == nil {
		t.Fatal("config load returned nil")
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route rule, got %d", len(cfg.Routes))
	}
	if cfg.Routes[0].Model != "test-model" {
		t.Fatalf("expected model test-model, got %q", cfg.Routes[0].Model)
	}
	if len(cfg.Routes[0].Routes) != 1 {
		t.Fatalf("expected 1 route spec, got %d", len(cfg.Routes[0].Routes))
	}
	spec := cfg.Routes[0].Routes[0]
	if spec.Provider != "test-provider" || spec.Model != "gpt-4" {
		t.Fatalf("expected provider=test-provider model=gpt-4, got provider=%q model=%q", spec.Provider, spec.Model)
	}
	if spec.Defaults.ReasoningEffort != "high" {
		t.Fatalf("expected reasoning_effort=high, got %q", spec.Defaults.ReasoningEffort)
	}
	if spec.Defaults.ThinkingBudget != 8192 {
		t.Fatalf("expected thinking_budget=8192, got %d", spec.Defaults.ThinkingBudget)
	}
}

func TestLoadYAML_RoutesInProvidersNotParsed(t *testing.T) {
	yaml := `
providers:
  - name: real-provider
    api_key: sk-real
    base_url: https://example.com
    style: openai
    auth_mode: bearer
  - model: fake-route
    routes:
      - provider: nowhere
        model: dummy
`
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("ROUTERLLM_CONFIG_FILE", f)
	defer os.Unsetenv("ROUTERLLM_CONFIG_FILE")

	cfg := Load()
	if cfg == nil {
		t.Fatal("config load returned nil")
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers (one real + one garbage), got %d", len(cfg.Providers))
	}
	if len(cfg.Routes) != 0 {
		t.Fatalf("expected 0 routes (routes nested under providers), got %d", len(cfg.Routes))
	}
}

func TestPortDefault(t *testing.T) {
	yaml := `
providers:
  - name: test
    api_key: sk-test
    base_url: https://example.com
    style: openai
    auth_mode: bearer
`
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("ROUTERLLM_CONFIG_FILE", f)
	defer os.Unsetenv("ROUTERLLM_CONFIG_FILE")

	cfg := Load()
	if cfg == nil {
		t.Fatal("config load returned nil")
	}
	if cfg.Port != "1765" {
		t.Fatalf("expected default port 1765, got %q", cfg.Port)
	}
}

func TestPortCustom(t *testing.T) {
	yaml := `
port: "8080"
providers:
  - name: test
    api_key: sk-test
    base_url: https://example.com
    style: openai
    auth_mode: bearer
`
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("ROUTERLLM_CONFIG_FILE", f)
	defer os.Unsetenv("ROUTERLLM_CONFIG_FILE")

	cfg := Load()
	if cfg == nil {
		t.Fatal("config load returned nil")
	}
	if cfg.Port != "8080" {
		t.Fatalf("expected port 8080, got %q", cfg.Port)
	}
}
