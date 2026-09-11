package config

import (
	"fmt"
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

func TestLoadYAMAlysisProviderReadsAccountsFile(t *testing.T) {
	accounts := filepath.Join(t.TempDir(), "alysis-accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"accounts":[{"accountId":"acc_1","gatewayKey":"slk_test1234","createdAt":"2026-01-01T00:00:00Z"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALYSIS_ACCOUNTS_FILE", accounts)

	path := writeTempYAML(t, `
providers:
  - name: alysis
    style: alysis
    base_url: https://gateway.alysis.test/v1
routes:
  - model_id: alysis-model
    routes:
      - provider: alysis
        model: alysis-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Providers[0].Keys) != 1 || cfg.Providers[0].Keys[0] != "slk_test1234" {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
}

func TestLoadYAMAlysisProviderWithoutAccountsFails(t *testing.T) {
	t.Setenv("ALYSIS_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "missing.json"))

	path := writeTempYAML(t, `
providers:
  - name: alysis
    style: alysis
    base_url: https://gateway.alysis.test/v1
routes:
  - model_id: alysis-model
    routes:
      - provider: alysis
        model: alysis-model
`)

	if _, err := loadYAML(path); err == nil || !strings.Contains(err.Error(), "no alysis accounts found") {
		t.Fatalf("error = %v, want 'no alysis accounts found'", err)
	}
}

func TestLoadYAMAlysisProviderAPIKeyWins(t *testing.T) {
	t.Setenv("ALYSIS_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "missing.json"))

	path := writeTempYAML(t, `
providers:
  - name: alysis
    style: alysis
    base_url: https://gateway.alysis.test/v1
    api_key: slk_explicit
routes:
  - model_id: alysis-model
    routes:
      - provider: alysis
        model: alysis-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Providers[0].Keys) != 1 || cfg.Providers[0].Keys[0] != "slk_explicit" {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
}

func TestLoadYAMLDisabledAlysisProviderWithoutAPIKey(t *testing.T) {
	t.Setenv("ALYSIS_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "missing.json"))

	path := writeTempYAML(t, `
providers:
  - name: alysis
    style: alysis
    base_url: https://gateway.alysis.test/v1
    disabled: true
routes:
  - model_id: alysis-model
    routes:
      - provider: alysis
        model: alysis-model
`)

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || !cfg.Providers[0].Disabled || len(cfg.Providers[0].Keys) != 0 {
		t.Fatalf("providers = %#v", cfg.Providers)
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

func TestLoadYAMLResolverEmptyEnvVarExpandsToNoKeys(t *testing.T) {
	t.Setenv("ROUTERLLM_TEST_EMPTY_KEY", "")

	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: ${ROUTERLLM_TEST_EMPTY_KEY}
routes:
  - model_id: m
    routes:
      - provider: test
        model: m
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "zero keys") {
		t.Fatalf("expected 'zero keys' error, got: %v", err)
	}
}

func TestLoadYAMLResolverSetEnvVarExpandsKeys(t *testing.T) {
	t.Setenv("ROUTERLLM_TEST_MULTI_KEY", "sk-a, sk-b")

	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: ${ROUTERLLM_TEST_MULTI_KEY}
routes:
  - model_id: m
    routes:
      - provider: test
        model: m
`)
	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.Providers[0].Keys; len(got) != 2 || got[0] != "sk-a" || got[1] != "sk-b" {
		t.Fatalf("keys = %#v, want [sk-a sk-b]", got)
	}
}

func TestLoadYAMLDisabledProviderEmptyEnvVarIsAllowed(t *testing.T) {
	t.Setenv("ROUTERLLM_TEST_EMPTY_KEY", "")

	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: ${ROUTERLLM_TEST_EMPTY_KEY}
    disabled: true
routes:
  - model_id: m
    routes:
      - provider: test
        model: m
`)
	if _, err := loadYAML(path); err != nil {
		t.Fatalf("disabled provider error = %v, want nil", err)
	}
}

func TestLoadYAMLDuplicateRouteModelIDRejected(t *testing.T) {
	path := writeTempYAML(t, `
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: m
    routes:
      - provider: test
        model: first
  - model_id: m
    routes:
      - provider: test
        model: second
`)
	_, err := loadYAML(path)
	if err == nil || !strings.Contains(err.Error(), `duplicate route model_id "m"`) {
		t.Fatalf("expected 'duplicate route model_id' error, got: %v", err)
	}
}

func TestLoadYAMLSystemPromptFileRelativeToConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "system_prompt.txt"), []byte("  be nice  \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "routerllm.yaml")
	if err := os.WriteFile(path, []byte(`
system_prompt_file: system_prompt.txt
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
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SystemPrompt != "be nice" {
		t.Fatalf("system prompt = %q, want %q", cfg.SystemPrompt, "be nice")
	}
}

func TestLoadYAMLSystemPromptFileAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("absolute prompt"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := writeTempYAML(t, fmt.Sprintf(`
system_prompt_file: %s
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
`, promptPath))

	cfg, err := loadYAML(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SystemPrompt != "absolute prompt" {
		t.Fatalf("system prompt = %q, want %q", cfg.SystemPrompt, "absolute prompt")
	}
}

func TestLoadYAMLInvalidPortRejected(t *testing.T) {
	for _, port := range []string{"not-a-port", "0", "-1", "65536"} {
		path := writeTempYAML(t, fmt.Sprintf(`
port: %q
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
`, port))

		_, err := loadYAML(path)
		if err == nil || !strings.Contains(err.Error(), "invalid port") {
			t.Errorf("port %q: error = %v, want invalid port error", port, err)
		}
	}
}

func TestLoadYAMLValidPortAccepted(t *testing.T) {
	for _, port := range []string{"1", "1765", "65535"} {
		path := writeTempYAML(t, fmt.Sprintf(`
port: %q
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
`, port))

		cfg, err := loadYAML(path)
		if err != nil {
			t.Errorf("port %q: unexpected error: %v", port, err)
			continue
		}
		if cfg.Port != port {
			t.Errorf("port = %q, want %q", cfg.Port, port)
		}
	}
}

func TestLoadRejectsInvalidPortEnvironment(t *testing.T) {
	path := writeTempYAML(t, `
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
	t.Setenv("ROUTERLLM_CONFIG_FILE", path)
	t.Setenv("ROUTERLLM_PORT", "not-a-port")

	if cfg := Load(); cfg != nil {
		t.Fatalf("Load() = %+v, want nil for an invalid ROUTERLLM_PORT", cfg)
	}
}

func TestValidateBytesMatchesLoadFileValidation(t *testing.T) {
	valid := []byte(`
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
	if err := ValidateBytes(valid); err != nil {
		t.Fatalf("ValidateBytes(valid) error = %v, want nil", err)
	}

	broken := []byte(`
providers:
  - name: test
    style: openai
    base_url: https://example.com
    api_key: sk-test
routes:
  - model_id: m
    routes:
      - provider: ghost
        model: m
`)
	if err := ValidateBytes(broken); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("ValidateBytes(broken) error = %v, want unknown provider", err)
	}
}

func TestValidateBytesRejectsUnreadableSystemPromptFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-prompt.txt")
	data := []byte(fmt.Sprintf(`
system_prompt_file: %s
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
`, missing))

	if err := ValidateBytes(data); err == nil || !strings.Contains(err.Error(), "system_prompt_file") {
		t.Fatalf("ValidateBytes() error = %v, want unreadable system_prompt_file", err)
	}
}
