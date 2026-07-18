package config

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"routerllm/internal/routing"
	"routerllm/internal/util"
)

type ProviderConfig struct {
	Name      string
	BaseURL   string
	Style     string
	Keys      []string
	Headers   map[string]string
	ShareKeys string
	AuthMode  string
	Query     string
}

type Config struct {
	Port       string
	Cooldown   time.Duration
	RoutesFile string
	Providers  []ProviderConfig
	Client     *http.Client
	Routes     []routing.Rule
}

func Load() *Config {
	_ = util.LoadDotenv(".env")

	configFile := os.Getenv("ROUTERLLM_CONFIG_FILE")
	if configFile == "" {
		configFile = "routerllm.yaml"
	}
	if cfg, err := loadYAML(configFile); err == nil {
		return cfg
	}

	return loadEnv()
}

func loadEnv() *Config {
	port := os.Getenv("ROUTERLLM_PORT")
	if port == "" {
		port = "1765"
	}

	cooldown := 60 * time.Second
	if v := os.Getenv("AGENTROUTER_KEY_COOLDOWN"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cooldown = d
		}
	}

	routesFile := os.Getenv("ROUTERLLM_ROUTES_FILE")
	if routesFile == "" {
		routesFile = "routes.json"
	}

	keysOverrideFile := os.Getenv("ROUTERLLM_KEYS_OVERRIDE_FILE")
	if keysOverrideFile == "" {
		keysOverrideFile = "keys.override.json"
	}

	var providers []ProviderConfig

	if keys := splitList(os.Getenv("AGENT_ROUTER_API_KEY")); len(keys) > 0 {
		providers = append(providers, ProviderConfig{
			Name:    "agentrouter",
			BaseURL: envOr("AGENTROUTER_TARGET", "https://agentrouter.org"),
			Style:   "openai",
			Keys:    keys,
			Headers: stainlessHeaders(),
		})
	}

	if key := os.Getenv("OPENCODE_API_KEY"); key != "" {
		providers = append(providers, ProviderConfig{
			Name:     "opencode",
			BaseURL:  envOr("OPENCODE_BASE_URL", "https://opencode.ai/zen"),
			Style:    "openai",
			Keys:     []string{key},
			Headers:  map[string]string{"Content-Type": "application/json"},
			AuthMode: "bearer",
		})
	}

	if key := os.Getenv("ALIBABA_API_KEY"); key != "" {
		providers = append(providers, ProviderConfig{
			Name:     "alibaba",
			BaseURL:  envOr("ALIBABA_BASE_URL", "https://ws-1h0fygqzbmuhg6j1.ap-southeast-1.maas.aliyuncs.com/compatible-mode"),
			Style:    "openai",
			Keys:     []string{key},
			Headers:  map[string]string{"Content-Type": "application/json"},
			AuthMode: "bearer",
		})
	}

	freemodelKeys := splitList(os.Getenv("FREEMODEL_API_KEY"))
	if len(freemodelKeys) > 0 {
		fmHeaders := freemodelHeaders()
		providers = append(providers, ProviderConfig{
			Name:      "freemodel-api",
			BaseURL:   envOr("FREEMODEL_API_BASE_URL", "https://api.freemodel.dev"),
			Style:     "openai",
			Keys:      freemodelKeys,
			Headers:   fmHeaders,
			ShareKeys: "freemodel",
			AuthMode:  "both",
			Query:     "?beta=true",
		})
		providers = append(providers, ProviderConfig{
			Name:      "freemodel-cc",
			BaseURL:   envOr("FREEMODEL_CC_BASE_URL", "https://cc.freemodel.dev"),
			Style:     "anthropic",
			Keys:      freemodelKeys,
			Headers:   fmHeaders,
			ShareKeys: "freemodel",
			AuthMode:  "both",
			Query:     "?beta=true",
		})
	}

	if keys := splitList(os.Getenv("AEROLINK_API_KEY")); len(keys) > 0 {
		providers = append(providers, ProviderConfig{
			Name:     "aerolink",
			BaseURL:  envOr("AEROLINK_BASE_URL", "https://capi.aerolink.lat"),
			Style:    "anthropic",
			Keys:     keys,
			Headers:  aerolinkHeaders(),
			AuthMode: "x-api-key",
		})
	}

	if keys := splitList(os.Getenv("FORGE_API_KEY")); len(keys) > 0 {
		providers = append(providers, ProviderConfig{
			Name:     "forge",
			BaseURL:  envOr("FORGE_BASE_URL", "https://forge-gateway-api.fly.dev"),
			Style:    "openai",
			Keys:     keys,
			Headers:  map[string]string{"Content-Type": "application/json"},
			AuthMode: "bearer",
		})
	}

	client := &http.Client{Transport: newTransport()}

	if err := applyKeyOverrides(keysOverrideFile, providers); err != nil {
		log.Printf("warning: failed to apply key overrides from %s: %v", keysOverrideFile, err)
	}

	return &Config{Port: port, Cooldown: cooldown, RoutesFile: routesFile, Providers: providers, Client: client}
}

func newTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 600 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func applyKeyOverrides(path string, providers []ProviderConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var overrides map[string][]string
	if err := json.Unmarshal(data, &overrides); err != nil {
		return err
	}
	for i := range providers {
		key := providers[i].ShareKeys
		if key == "" {
			key = providers[i].Name
		}
		if ks, ok := overrides[key]; ok {
			providers[i].Keys = ks
		}
	}
	return nil
}

func envOr(key, def string) string {
	v := strings.TrimRight(os.Getenv(key), "/")
	v = strings.TrimSuffix(v, "/v1")
	if v == "" {
		return def
	}
	return v
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		s := strings.TrimSpace(part)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stainlessHeaders() map[string]string {
	return map[string]string{
		"Content-Type":                "application/json",
		"User-Agent":                  "RooCode/3.53.0",
		"X-Stainless-OS":              "Linux",
		"X-Stainless-Arch":            "x64",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": "v22.22.1",
		"HTTP-Referer":                "https://github.com/RooVetGit/Roo-Cline",
		"X-Title":                     "Continue",
	}
}

func aerolinkHeaders() map[string]string {
	return map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
	}
}

func freemodelHeaders() map[string]string {
	return map[string]string{
		"Accept":                                    "application/json",
		"Content-Type":                              "application/json",
		"User-Agent":                                "claude-cli/2.1.207 (external, cli)",
		"X-Stainless-Arch":                          "x64",
		"X-Stainless-Lang":                          "js",
		"X-Stainless-OS":                            "Linux",
		"X-Stainless-Package-Version":               "0.94.0",
		"X-Stainless-Retry-Count":                   "0",
		"X-Stainless-Runtime":                       "node",
		"X-Stainless-Runtime-Version":               "v26.3.0",
		"X-Stainless-Timeout":                       "600",
		"anthropic-beta":                            "claude-code-20250219,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24",
		"anthropic-dangerous-direct-browser-access": "true",
		"anthropic-version":                         "2023-06-01",
		"x-app":                                     "cli",
	}
}
