package provider

import (
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
)

func testConfigs(disabled bool) []config.ProviderConfig {
	return []config.ProviderConfig{
		{Name: "alpha", BaseURL: "https://alpha.example", Style: "openai", Keys: []string{"key-1", "key-2"}},
		{Name: "beta", BaseURL: "https://beta.example", Style: "openai", Keys: []string{"key-3"}, Disabled: disabled},
	}
}

func testRules() []model.Rule {
	return []model.Rule{
		{ModelID: "model-a", Routes: []model.Spec{{Provider: "alpha", Model: "upstream-a"}}},
		{ModelID: "model-b", Routes: []model.Spec{{Provider: "beta", Model: "upstream-b"}}},
	}
}

func TestNewRegistrySkipsDisabledProvider(t *testing.T) {
	reg := NewRegistry(testConfigs(true), testRules(), time.Minute)

	if got := reg.ActiveProviders(); got != 1 {
		t.Fatalf("ActiveProviders() = %d, want 1", got)
	}
	if got := reg.TotalProviders(); got != 2 {
		t.Fatalf("TotalProviders() = %d, want 2", got)
	}
	if models := reg.AllModels(); len(models) != 1 || models[0] != "model-a" {
		t.Fatalf("AllModels() = %v, want [model-a]", models)
	}
	if skipped := reg.SkippedRoutes(); len(skipped) != 1 {
		t.Fatalf("SkippedRoutes() = %v, want 1 entry", skipped)
	}
}

func TestSkippedRoutesIsNeverNil(t *testing.T) {
	reg := NewRegistry(testConfigs(false), testRules(), time.Minute)

	skipped := reg.SkippedRoutes()
	if skipped == nil {
		t.Fatal("SkippedRoutes() = nil; the admin status payload must encode [] so the console can read .length")
	}
	if len(skipped) != 0 {
		t.Fatalf("SkippedRoutes() = %v, want empty", skipped)
	}
}

func TestNewRegistrySkipsDisabledRouteEntry(t *testing.T) {
	rules := []model.Rule{{ModelID: "model-chain", Routes: []model.Spec{
		{Provider: "alpha", Model: "upstream-a", Disabled: true},
		{Provider: "beta", Model: "upstream-b"},
	}}}

	reg := NewRegistry(testConfigs(false), rules, time.Minute)

	routes := reg.Routes("model-chain")
	if len(routes) != 1 {
		t.Fatalf("Routes() returned %d routes, want 1", len(routes))
	}
	if routes[0].Provider.Name != "beta" {
		t.Fatalf("surviving route provider = %q, want beta", routes[0].Provider.Name)
	}
}

func TestNewRegistryDropsModelWhenEveryRouteDisabled(t *testing.T) {
	rules := []model.Rule{{ModelID: "model-off", Routes: []model.Spec{
		{Provider: "alpha", Model: "upstream-a", Disabled: true},
	}}}

	reg := NewRegistry(testConfigs(false), rules, time.Minute)

	if models := reg.AllModels(); len(models) != 0 {
		t.Fatalf("AllModels() = %v, want empty", models)
	}
	if routes := reg.Routes("model-off"); routes != nil {
		t.Fatalf("Routes() = %v, want nil", routes)
	}
}

func TestRebuildCarriesOverKeyCooldown(t *testing.T) {
	before := NewRegistry(testConfigs(false), testRules(), time.Minute)
	alpha := before.providers["alpha"]
	alpha.Keys.MarkDead("key-1")

	if got := alpha.Keys.AliveCount(); got != 1 {
		t.Fatalf("pre-rebuild AliveCount() = %d, want 1", got)
	}

	after := Rebuild(testConfigs(false), testRules(), time.Minute, before)

	if got := after.providers["alpha"].Keys.AliveCount(); got != 1 {
		t.Fatalf("post-rebuild AliveCount() = %d, want 1 (cooldown should carry over)", got)
	}
	if got := after.providers["beta"].Keys.AliveCount(); got != 1 {
		t.Fatalf("untouched provider AliveCount() = %d, want 1", got)
	}
}

func TestRebuildFromNilPreviousRegistry(t *testing.T) {
	reg := Rebuild(testConfigs(false), testRules(), time.Minute, nil)

	if got := reg.ActiveProviders(); got != 2 {
		t.Fatalf("ActiveProviders() = %d, want 2", got)
	}
}

func TestRebuildDoesNotReviveDeadKeyAcrossGenerations(t *testing.T) {
	gen1 := NewRegistry(testConfigs(false), testRules(), time.Hour)
	gen1.providers["alpha"].Keys.MarkDead("key-1")

	gen2 := Rebuild(testConfigs(false), testRules(), time.Hour, gen1)
	gen3 := Rebuild(testConfigs(false), testRules(), time.Hour, gen2)

	if got := gen3.providers["alpha"].Keys.AliveCount(); got != 1 {
		t.Fatalf("gen3 AliveCount() = %d, want 1", got)
	}
}
