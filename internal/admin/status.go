package admin

import (
	"time"

	"routerllm/internal/provider"
)

type KeyState struct {
	Masked       string `json:"masked"`
	Alive        bool   `json:"alive"`
	CooldownLeft int    `json:"cooldown_left_seconds"`
}

type ProviderStatus struct {
	Name       string     `json:"name"`
	Style      string     `json:"style"`
	BaseURL    string     `json:"base_url"`
	AuthMode   string     `json:"auth_mode"`
	Disabled   bool       `json:"disabled"`
	KeysAlive  int        `json:"keys_alive"`
	KeysTotal  int        `json:"keys_total"`
	Keys       []KeyState `json:"keys"`
	Requests   uint64     `json:"requests"`
	Errors     uint64     `json:"errors"`
	Shared     string     `json:"share,omitempty"`
	Serving    bool       `json:"serving"`
	ModelCount int        `json:"model_count"`
}

type RouteLeg struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Disabled         bool   `json:"disabled"`
	Active           bool   `json:"active"`
	ProviderDisabled bool   `json:"provider_disabled"`
}

type ModelStatus struct {
	ModelID string     `json:"model_id"`
	Serving bool       `json:"serving"`
	Chain   []RouteLeg `json:"chain"`
}

type Status struct {
	UptimeSeconds   int              `json:"uptime_seconds"`
	ConfigPath      string           `json:"config_path"`
	LastReload      ReloadStatus     `json:"last_reload"`
	ProvidersTotal  int              `json:"providers_total"`
	ProvidersActive int              `json:"providers_active"`
	ModelsServing   int              `json:"models_serving"`
	Providers       []ProviderStatus `json:"providers"`
	Models          []ModelStatus    `json:"models"`
	SkippedRoutes   []string         `json:"skipped_routes"`
}

func (d Deps) buildStatus() Status {
	reg := d.Registry()
	providers := buildProviders(reg)
	models := buildModels(reg)

	serving := 0
	for _, m := range models {
		if m.Serving {
			serving++
		}
	}

	countModelsPerProvider(providers, models)

	return Status{
		UptimeSeconds:   int(time.Since(d.StartedAt).Seconds()),
		ConfigPath:      d.Editor.Path(),
		LastReload:      d.Reloads.Last(),
		ProvidersTotal:  reg.TotalProviders(),
		ProvidersActive: reg.ActiveProviders(),
		ModelsServing:   serving,
		Providers:       providers,
		Models:          models,
		SkippedRoutes:   reg.SkippedRoutes(),
	}
}

func buildProviders(reg *provider.Registry) []ProviderStatus {
	out := make([]ProviderStatus, 0, reg.TotalProviders())

	for _, pc := range reg.ProviderConfigs() {
		status := ProviderStatus{
			Name:     pc.Name,
			Style:    pc.Style,
			BaseURL:  pc.BaseURL,
			AuthMode: pc.AuthMode,
			Disabled: pc.Disabled,
			Shared:   pc.ShareKeys,
			Keys:     []KeyState{},
		}

		if live, ok := reg.Provider(pc.Name); ok {
			status.Serving = true
			status.KeysAlive = live.Keys.AliveCount()
			status.Requests = live.Stats().Requests()
			status.Errors = live.Stats().Errors()
			status.Keys = keyStates(live)
		}
		status.KeysTotal = len(pc.Keys)

		out = append(out, status)
	}

	return out
}

func keyStates(p *provider.Provider) []KeyState {
	states := p.Keys.States()
	out := make([]KeyState, 0, len(states))

	for _, s := range states {
		left := 0
		if !s.Alive {
			left = int(time.Until(s.DeadUntil).Seconds()) + 1
			if left < 0 {
				left = 0
			}
		}
		out = append(out, KeyState{Masked: s.Masked, Alive: s.Alive, CooldownLeft: left})
	}

	return out
}

func buildModels(reg *provider.Registry) []ModelStatus {
	rules := reg.Rules()
	out := make([]ModelStatus, 0, len(rules))

	for _, rule := range rules {
		model := ModelStatus{ModelID: rule.ModelID, Chain: make([]RouteLeg, 0, len(rule.Routes))}
		activeAssigned := false

		for _, spec := range rule.Routes {
			_, providerLive := reg.Provider(spec.Provider)
			leg := RouteLeg{
				Provider:         spec.Provider,
				Model:            spec.Model,
				Disabled:         spec.Disabled,
				ProviderDisabled: !providerLive,
			}

			if !spec.Disabled && providerLive && !activeAssigned {
				leg.Active = true
				activeAssigned = true
			}
			model.Chain = append(model.Chain, leg)
		}

		model.Serving = activeAssigned
		out = append(out, model)
	}

	return out
}

func countModelsPerProvider(providers []ProviderStatus, models []ModelStatus) {
	counts := make(map[string]int, len(providers))
	for _, m := range models {
		for _, leg := range m.Chain {
			if !leg.Disabled {
				counts[leg.Provider]++
			}
		}
	}

	for i := range providers {
		providers[i].ModelCount = counts[providers[i].Name]
	}
}
