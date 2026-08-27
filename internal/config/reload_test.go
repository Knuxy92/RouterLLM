package config

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const validConfig = `
port: 17999
providers:
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
    auth_mode: bearer
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadFileAllowsDisabledProviderWithoutAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: parked
    disabled: true
    base_url: https://parked.example
    style: openai
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v, want nil (disabled provider needs no api_key)", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("Providers len = %d, want 2", len(cfg.Providers))
	}
	if !cfg.Providers[0].Disabled {
		t.Fatal("parked provider should be marked Disabled")
	}
}

func TestLoadFileAllowsDisabledClineWithoutAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: cline
    disabled: true
    base_url: https://api.cline.bot/api
    style: cline
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`)

	if _, err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error = %v, want nil (disabled cline needs no accounts)", err)
	}
}

func TestLoadFileStillRejectsEnabledProviderWithoutAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("LoadFile() error = %v, want api_key required error", err)
	}
}

func TestLoadFileAcceptsRouteReferencingDisabledProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: parked
    disabled: true
    base_url: https://parked.example
    style: openai
routes:
  - model_id: model-p
    routes:
      - provider: parked
        model: upstream-p
`)

	if _, err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error = %v, want nil (route may reference a disabled provider)", err)
	}
}

func TestLoadFileRejectsRouteReferencingUnknownProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-ghost
    routes:
      - provider: ghost
        model: upstream-x
`)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("LoadFile() error = %v, want unknown provider error", err)
	}
}

func TestLoadFileParsesRouteLevelDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, `
providers:
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
        disabled: true
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if !cfg.Routes[0].Routes[0].Disabled {
		t.Fatal("route entry should be marked Disabled")
	}
}

func TestWatchAppliesValidChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, validConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	applied := make(chan *Config, 1)
	reloader := NewReloader(path, Hash(path), log.New(os.Stderr, "", 0), func(cfg *Config) {
		applied <- cfg
	}, nil)
	go reloader.Watch(ctx)

	writeConfig(t, path, strings.Replace(validConfig, "model-a", "model-renamed", 1))

	select {
	case cfg := <-applied:
		if cfg.Routes[0].ModelID != "model-renamed" {
			t.Fatalf("applied ModelID = %q, want model-renamed", cfg.Routes[0].ModelID)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for reload")
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestWatchRejectsInvalidChangeAndKeepsWatching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, validConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	applied := make(chan *Config, 1)
	rejected := make(chan error, 1)
	logs := &syncBuffer{}
	reloader := NewReloader(path, Hash(path), log.New(logs, "", 0), func(cfg *Config) {
		applied <- cfg
	}, func(err error) {
		rejected <- err
	})
	go reloader.Watch(ctx)

	writeConfig(t, path, "providers:\n  - name: broken\n    bad: [unclosed\n")

	select {
	case cfg := <-applied:
		t.Fatalf("apply called for invalid config: %+v", cfg)
	case <-time.After(8 * time.Second):
	}

	select {
	case err := <-rejected:
		if err == nil {
			t.Fatal("reject callback received nil error")
		}
	default:
		t.Fatal("reject callback was never invoked")
	}

	if !strings.Contains(logs.String(), "config reload rejected") {
		t.Fatalf("logs = %q, want rejection message", logs.String())
	}

	writeConfig(t, path, strings.Replace(validConfig, "model-a", "model-recovered", 1))

	select {
	case cfg := <-applied:
		if cfg.Routes[0].ModelID != "model-recovered" {
			t.Fatalf("applied ModelID = %q, want model-recovered", cfg.Routes[0].ModelID)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("watcher stopped after a rejected reload")
	}
}

func TestReloadIsIdempotentForUnchangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	writeConfig(t, path, validConfig)

	var applies int
	reloader := NewReloader(path, Hash(path), log.New(os.Stderr, "", 0), func(*Config) {
		applies++
	}, nil)

	if err := reloader.Reload(); err != nil {
		t.Fatalf("Reload() on unchanged file error = %v", err)
	}
	if applies != 0 {
		t.Fatalf("applies = %d, want 0 for unchanged content", applies)
	}

	writeConfig(t, path, strings.Replace(validConfig, "model-a", "model-b", 1))

	if err := reloader.Reload(); err != nil {
		t.Fatalf("Reload() after change error = %v", err)
	}
	if err := reloader.Reload(); err != nil {
		t.Fatalf("second Reload() error = %v", err)
	}
	if applies != 1 {
		t.Fatalf("applies = %d, want 1 — a single content change must apply once", applies)
	}
}

func TestConfigPathHonoursEnv(t *testing.T) {
	t.Setenv("ROUTERLLM_CONFIG_FILE", "custom.yaml")
	if got := ConfigPath(); got != "custom.yaml" {
		t.Fatalf("ConfigPath() = %q, want custom.yaml", got)
	}

	t.Setenv("ROUTERLLM_CONFIG_FILE", "")
	if got := ConfigPath(); got != "routerllm.yaml" {
		t.Fatalf("ConfigPath() = %q, want routerllm.yaml", got)
	}
}
