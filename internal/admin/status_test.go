package admin

import (
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
	"routerllm/internal/quota"
)

func TestBuildProvidersAttachesKeyQuota(t *testing.T) {
	reg := provider.NewRegistry([]config.ProviderConfig{{
		Name: "codex", BaseURL: "https://chatgpt.com/backend-api", Style: "codex",
		Keys: []string{"rt-1", "rt-2"},
	}}, []model.Rule{{
		ModelID: "m", Routes: []model.Spec{{Provider: "codex", Model: "m"}},
	}}, time.Minute)

	quotaFn := func(name, key string) (quota.Snapshot, bool) {
		if name != "codex" || key != "rt-1" {
			return quota.Snapshot{}, false
		}

		return quota.Snapshot{
			Primary:  &quota.Window{UsedPercent: 42, WindowMinutes: 43200, ResetAt: 1792294116},
			PlanType: "free",
		}, true
	}

	providers := buildProviders(reg, quotaFn)
	if len(providers) != 1 || len(providers[0].Keys) != 2 {
		t.Fatalf("providers = %+v", providers)
	}
	if q := providers[0].Keys[0].Quota; q == nil || q.Primary == nil || q.Primary.UsedPercent != 42 || q.PlanType != "free" {
		t.Fatalf("keys[0].quota = %+v", providers[0].Keys[0].Quota)
	}
	if providers[0].Keys[1].Quota != nil {
		t.Fatalf("keys[1].quota = %+v, want nil", providers[0].Keys[1].Quota)
	}

	providers = buildProviders(reg, nil)
	if providers[0].Keys[0].Quota != nil {
		t.Fatalf("nil quota func must leave keys bare: %+v", providers[0].Keys[0].Quota)
	}
}
