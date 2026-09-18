package services

import (
	"io"
	"log"
	"testing"
	"time"

	"routerllm/internal/codex"
	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

// The cline manager map is keyed by provider base URL and lives on Proxy, which
// outlives every registry generation. Without pruning, repointing or removing a
// cline provider retains its manager — and its refresh-token cache — forever.
func TestApplyPrunesClineManagersForRemovedProviders(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "cline-model",
		Routes:  []model.Spec{{Provider: "cline", Model: "upstream"}},
	}}
	first := provider.NewRegistry([]config.ProviderConfig{{
		Name: "cline", BaseURL: "https://old.example", Style: "cline", Keys: []string{"refresh-1"},
	}}, rules, time.Minute)

	proxy := NewProxy(first, nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.clineMu.Lock()
	proxy.clineManagers["https://old.example"] = nil
	proxy.clineMu.Unlock()

	moved := provider.NewRegistry([]config.ProviderConfig{{
		Name: "cline", BaseURL: "https://new.example", Style: "cline", Keys: []string{"refresh-1"},
	}}, rules, time.Minute)
	proxy.Apply(moved, "")

	proxy.clineMu.Lock()
	_, stale := proxy.clineManagers["https://old.example"]
	total := len(proxy.clineManagers)
	proxy.clineMu.Unlock()

	if stale {
		t.Fatal("manager for the removed base URL survived Apply")
	}
	if total != 0 {
		t.Fatalf("clineManagers len = %d, want 0", total)
	}
}

func TestApplyKeepsClineManagerForStillConfiguredProvider(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "cline-model",
		Routes:  []model.Spec{{Provider: "cline", Model: "upstream"}},
	}}
	configs := []config.ProviderConfig{{
		Name: "cline", BaseURL: "https://live.example", Style: "cline", Keys: []string{"refresh-1"},
	}}

	proxy := NewProxy(provider.NewRegistry(configs, rules, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.clineMu.Lock()
	proxy.clineManagers["https://live.example"] = nil
	proxy.clineMu.Unlock()

	proxy.Apply(provider.NewRegistry(configs, rules, time.Minute), "")

	proxy.clineMu.Lock()
	_, kept := proxy.clineManagers["https://live.example"]
	proxy.clineMu.Unlock()

	if !kept {
		t.Fatal("manager for a still-configured base URL was pruned")
	}
}

func TestApplyPrunesClineManagerForDisabledProvider(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "cline-model",
		Routes:  []model.Spec{{Provider: "cline", Model: "upstream"}},
	}}
	enabled := []config.ProviderConfig{{
		Name: "cline", BaseURL: "https://parked.example", Style: "cline", Keys: []string{"refresh-1"},
	}}

	proxy := NewProxy(provider.NewRegistry(enabled, rules, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.clineMu.Lock()
	proxy.clineManagers["https://parked.example"] = nil
	proxy.clineMu.Unlock()

	parked := []config.ProviderConfig{{
		Name: "cline", BaseURL: "https://parked.example", Style: "cline", Keys: []string{"refresh-1"}, Disabled: true,
	}}
	proxy.Apply(provider.NewRegistry(parked, rules, time.Minute), "")

	proxy.clineMu.Lock()
	_, kept := proxy.clineManagers["https://parked.example"]
	proxy.clineMu.Unlock()

	if kept {
		t.Fatal("manager for a parked provider survived Apply")
	}
}

// The codex manager is a single shared instance on Proxy, which outlives every
// registry generation. Without pruning, removing the last codex provider keeps
// its token cache alive for the process lifetime.
func TestApplyPrunesCodexManagerWhenNoCodexProviderRemains(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "codex-model",
		Routes:  []model.Spec{{Provider: "codex", Model: "upstream"}},
	}}
	first := provider.NewRegistry([]config.ProviderConfig{{
		Name: "codex", BaseURL: "https://codex.example", Style: "codex", Keys: []string{"refresh-1"},
	}}, rules, time.Minute)

	proxy := NewProxy(first, nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	seedCodexManager(proxy, codex.NewManager(nil, nil))

	other := provider.NewRegistry([]config.ProviderConfig{{
		Name: "openai", BaseURL: "https://openai.example", Style: "openai", Keys: []string{"sk-1"},
	}}, rules, time.Minute)
	proxy.Apply(other, "")

	if manager := liveCodexManager(proxy); manager != nil {
		t.Fatal("codex manager survived Apply without a codex provider")
	}
}

func TestApplyKeepsCodexManagerForStillConfiguredProvider(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "codex-model",
		Routes:  []model.Spec{{Provider: "codex", Model: "upstream"}},
	}}
	configs := []config.ProviderConfig{{
		Name: "codex", BaseURL: "https://live.example", Style: "codex", Keys: []string{"refresh-1"},
	}}

	proxy := NewProxy(provider.NewRegistry(configs, rules, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	manager := codex.NewManager(nil, nil)
	seedCodexManager(proxy, manager)

	proxy.Apply(provider.NewRegistry(configs, rules, time.Minute), "")

	if kept := liveCodexManager(proxy); kept != manager {
		t.Fatal("codex manager for a still-configured provider was pruned")
	}
}

func TestApplyPrunesCodexManagerForDisabledProvider(t *testing.T) {
	rules := []model.Rule{{
		ModelID: "codex-model",
		Routes:  []model.Spec{{Provider: "codex", Model: "upstream"}},
	}}
	enabled := []config.ProviderConfig{{
		Name: "codex", BaseURL: "https://parked.example", Style: "codex", Keys: []string{"refresh-1"},
	}}

	proxy := NewProxy(provider.NewRegistry(enabled, rules, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	seedCodexManager(proxy, codex.NewManager(nil, nil))

	parked := []config.ProviderConfig{{
		Name: "codex", BaseURL: "https://parked.example", Style: "codex", Keys: []string{"refresh-1"}, Disabled: true,
	}}
	proxy.Apply(provider.NewRegistry(parked, rules, time.Minute), "")

	if manager := liveCodexManager(proxy); manager != nil {
		t.Fatal("codex manager for a parked provider survived Apply")
	}
}

func seedCodexManager(proxy *Proxy, manager *codex.Manager) {
	proxy.codexMu.Lock()
	proxy.codexManager = manager
	proxy.codexMu.Unlock()
}

func liveCodexManager(proxy *Proxy) *codex.Manager {
	proxy.codexMu.Lock()
	defer proxy.codexMu.Unlock()

	return proxy.codexManager
}
