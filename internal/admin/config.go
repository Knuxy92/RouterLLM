package admin

import (
	"net/http"
	"sort"

	"routerllm/internal/settings"
	"routerllm/internal/util"
)

// ConfigPayload is what the embedded console bundle reads at startup instead of
// hardcoding the same numbers in JavaScript. Every value comes from the live
// settings snapshot, so editing routerllm.yaml moves the server and the UI
// together on the next reload.
type ConfigPayload struct {
	UI           UIConfig   `json:"ui"`
	Test         TestConfig `json:"test"`
	Efforts      []string   `json:"efforts"`
	StyleCalls   []string   `json:"stylecalls"`
	RingCapacity int        `json:"ring_capacity"`
}

// UIConfig carries the console's own cadence and paging.
type UIConfig struct {
	PollMS     int `json:"poll_ms"`
	LogPerPage int `json:"log_per_page"`
}

// TestConfig mirrors the server-side clamps the provider-test panel enforces.
type TestConfig struct {
	PromptBytes    int    `json:"prompt_bytes"`
	MaxTokens      int    `json:"max_tokens"`
	DefaultTokens  int    `json:"default_tokens"`
	TimeoutFloor   int    `json:"timeout_floor"`
	TimeoutCeiling int    `json:"timeout_ceiling"`
	DefaultTimeout int    `json:"default_timeout"`
	DefaultPrompt  string `json:"default_prompt"`
}

func (d Deps) handleConfig(w http.ResponseWriter, r *http.Request) {
	live := settings.Current()

	util.WriteJSON(w, http.StatusOK, ConfigPayload{
		UI: UIConfig{
			PollMS:     live.Admin.UI.PollMS,
			LogPerPage: live.Admin.UI.LogPerPage,
		},
		Test: TestConfig{
			PromptBytes:    live.Admin.Test.PromptBytes,
			MaxTokens:      live.Admin.Test.MaxTokens,
			DefaultTokens:  live.Admin.Test.DefaultTokens,
			TimeoutFloor:   live.Admin.Test.TimeoutFloorSeconds,
			TimeoutCeiling: live.Admin.Test.TimeoutCeilingSeconds,
			DefaultTimeout: live.Admin.Test.DefaultTimeoutSeconds,
			DefaultPrompt:  live.Admin.Test.DefaultPrompt,
		},
		Efforts:      sortedKeys(validReasoningEffort),
		StyleCalls:   sortedKeys(validStyleCall),
		RingCapacity: live.Telemetry.RingCapacity,
	})
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}
