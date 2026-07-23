package config

import (
	"os"
	"strings"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "routerllm-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoadYAMLValid(t *testing.T) {
	path := writeTempYAML(t, `
port: "9999"
cooldown: 30s
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model: test-model
    routes:
      - provider: test
        model: upstream-model
`)
	
	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if cfg.Port != "9999" {
		t.Errorf("port = %q, want 9999", cfg.Port)
	}
}

func TestLoadYAMLMissingProviderName(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected 'empty name' error, got: %v", err)
	}
}

func TestLoadYAMLDuplicateProvider(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: dup
    style: openai
    base_url: https://a.com
    api_key: k1
  - name: dup
    style: anthropic
    base_url: https://b.com
    api_key: k2
routes:
  - model: m
    routes:
      - provider: dup
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected 'duplicate' error, got: %v", err)
	}
}

func TestLoadYAMLInvalidStyle(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: bad
    style: invalid
    base_url: https://example.com
    api_key: sk-test
routes:
  - model: m
    routes:
      - provider: bad
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported style") {
		t.Fatalf("expected 'unsupported style' error, got: %v", err)
	}
}

func TestLoadYAMLInvalidAuthMode(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: bad
    style: openai
    base_url: https://example.com
    api_key: sk-test
    auth_mode: magic
routes:
  - model: m
    routes:
      - provider: bad
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported auth_mode") {
		t.Fatalf("expected 'unsupported auth_mode' error, got: %v", err)
	}
}

func TestLoadYAMLInvalidCooldown(t *testing.T) {
	path := writeTempYAML(t, `
cooldown: not-a-duration
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "invalid cooldown") {
		t.Fatalf("expected 'invalid cooldown' error, got: %v", err)
	}
}

func TestLoadYAMLMissingAPIKey(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
routes:
  - model: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("expected 'api_key is required' error, got: %v", err)
	}
}

func TestLoadYAMLReferencedProviderNotFound(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: existing
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model: m
    routes:
      - provider: missing
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("expected 'unknown provider' error, got: %v", err)
	}
}

func TestLoadYAMLResolverUnsetEnvVar(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: ${DOES_NOT_EXIST_XYZ123}
routes:
  - model: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("expected 'environment variable' error, got: %v", err)
	}
}
