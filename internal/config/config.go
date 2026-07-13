package config

import (
	"net/http"
	"os"
	"strings"
	"time"
)

type ProviderConfig struct {
	Name      string
	BaseURL   string
	Style     string
	Keys      []string
	Headers   map[string]string
	ShareKeys string
}

type Config struct {
	Port      string
	Cooldown  time.Duration
	Providers []ProviderConfig
	Client    *http.Client
}

func Load() *Config {
	port := os.Getenv("AGENTROUTER_PORT")
	if port == "" {
		port = "8000"
	}

	cooldown := 60 * time.Second
	if v := os.Getenv("AGENTROUTER_KEY_COOLDOWN"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cooldown = d
		}
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
			Name:    "opencode",
			BaseURL: envOr("OPENCODE_BASE_URL", "https://opencode.ai/zen/v1"),
			Style:   "openai",
			Keys:    []string{key},
			Headers: map[string]string{"Content-Type": "application/json"},
		})
	}

	if key := os.Getenv("ALIBABA_API_KEY"); key != "" {
		providers = append(providers, ProviderConfig{
			Name:    "alibaba",
			BaseURL: envOr("ALIBABA_BASE_URL", "https://ws-o5dwmvrzdkoo3tu4.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"),
			Style:   "openai",
			Keys:    []string{key},
			Headers: map[string]string{"Content-Type": "application/json"},
		})
	}

	freemodelKey := os.Getenv("FREEMODEL_API_KEY")
	if freemodelKey != "" {
		providers = append(providers, ProviderConfig{
			Name:      "freemodel-api",
			BaseURL:   envOr("FREEMODEL_API_BASE_URL", "https://api.freemodel.dev/v1"),
			Style:     "openai",
			Keys:      []string{freemodelKey},
			Headers:   map[string]string{"Content-Type": "application/json"},
			ShareKeys: "freemodel",
		})
		providers = append(providers, ProviderConfig{
			Name:      "freemodel-cc",
			BaseURL:   envOr("FREEMODEL_CC_BASE_URL", "https://cc.freemodel.dev/v1"),
			Style:     "anthropic",
			Keys:      []string{freemodelKey},
			Headers:   map[string]string{"Content-Type": "application/json", "anthropic-version": "2023-06-01"},
			ShareKeys: "freemodel",
		})
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 600 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client := &http.Client{Transport: transport}

	return &Config{Port: port, Cooldown: cooldown, Providers: providers, Client: client}
}

func envOr(key, def string) string {
	v := strings.TrimRight(os.Getenv(key), "/")
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
