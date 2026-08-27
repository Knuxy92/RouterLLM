package config

import (
	"os"
	"path/filepath"
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
  - model_id: test-model
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
	if !cfg.ForwardClientHeaders {
		t.Error("forward_client_headers = false, want true by default")
	}
}

func TestLoadYAMLForwardClientHeadersCanBeDisabled(t *testing.T) {
	path := writeTempYAML(t, `
forward_client_headers: false
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: test-model
    routes:
      - provider: test
        model: upstream-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ForwardClientHeaders {
		t.Error("forward_client_headers = true, want false")
	}
}

func TestLoadYAMLAllowClientHeaders(t *testing.T) {
	path := writeTempYAML(t, `
allow_client_headers:
  - X-Request-ID
  - User-Agent
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: test-model
    routes:
      - provider: test
        model: upstream-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"X-Request-ID", "User-Agent"}
	if len(cfg.AllowClientHeaders) != 2 || cfg.AllowClientHeaders[0] != want[0] || cfg.AllowClientHeaders[1] != want[1] {
		t.Fatalf("allow_client_headers = %#v, want %#v", cfg.AllowClientHeaders, want)
	}
}

func TestLoadPortEnvironmentOverride(t *testing.T) {
	path := writeTempYAML(t, `
port: "9999"
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: test-model
    routes:
      - provider: test
        model: upstream-model
`)
	t.Setenv("ROUTERLLM_CONFIG_FILE", path)
	t.Setenv("ROUTERLLM_PORT", "8888")

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() returned nil")
	}
	if cfg.Port != "8888" {
		t.Fatalf("port = %q, want environment override 8888", cfg.Port)
	}
}

func TestLoadYAMLClineProviderReadsAccountsFile(t *testing.T) {
	accounts := filepath.Join(t.TempDir(), "cline-accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"accounts":[{"accountId":"acc_1","email":"a@example.test","refreshToken":"refresh-1"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLINE_ACCOUNTS_FILE", accounts)

	path := writeTempYAML(t, `
providers:
  - name: cline
    style: cline
    base_url: https://api.cline.bot/api
routes:
  - model_id: cline-free/glm-5.2
    routes:
      - provider: cline
        model: cline-free/glm-5.2
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Providers[0].Keys) != 1 || cfg.Providers[0].Keys[0] != "refresh-1" {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
}

func TestLoadYAMLClineProviderWithoutAccountsFails(t *testing.T) {
	t.Setenv("CLINE_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "missing.json"))

	path := writeTempYAML(t, `
providers:
  - name: cline
    style: cline
    base_url: https://api.cline.bot/api
routes:
  - model_id: cline-free/glm-5.2
    routes:
      - provider: cline
        model: cline-free/glm-5.2
`)

	if _, err := loadYAML(path); err == nil || !strings.Contains(err.Error(), "--cline-login") {
		t.Fatalf("error = %v, want cline-login hint", err)
	}
}

func TestLoadYAMLAutoModelKeyIsIgnored(t *testing.T) {
	path := writeTempYAML(t, `
auto_model:
  enabled: true
  model: some-model
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: test-model
    routes:
      - provider: test
        model: upstream-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].ModelID != "test-model" {
		t.Fatalf("routes = %+v, want the configured route to load unaffected", cfg.Routes)
	}
}

func TestLoadYAMLMissingProviderName(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: m
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
  - model_id: m
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
  - model_id: m
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
  - model_id: m
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
  - model_id: m
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
  - model_id: m
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
  - model_id: m
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
  - model_id: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("expected 'environment variable' error, got: %v", err)
	}
}
