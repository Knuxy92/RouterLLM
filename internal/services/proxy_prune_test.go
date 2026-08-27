package services

import (
	"io"
	"log"
	"testing"
	"time"

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
