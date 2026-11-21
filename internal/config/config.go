package config

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"routerllm/internal/model"
	"routerllm/internal/relay"
	"routerllm/internal/settings"
)

type ProviderConfig struct {
	Name           string
	BaseURL        string
	Style          string
	Keys           []string
	Headers        map[string]string
	ShareKeys      string
	AuthMode       string
	Query          string
	ReasoningStyle string
	Disabled       bool
}

type Config struct {
	Port                 string
	Cooldown             time.Duration
	ForceStream          bool
	DedupeTools          bool
	ClampToolSchemas     bool
	ToolSchemaMaxDepth   int
	ForwardClientHeaders bool
	AllowClientHeaders   []string
	SystemPrompt         string
	OpencodeSystemPrompt string
	Settings             settings.Settings
	Providers            []ProviderConfig
	Client               *http.Client
	Routes               []model.Rule
}

func Load() *Config {
	cfg, err := loadYAML(ConfigPath())
	if err != nil {
		log.Printf("config error: %v", err)
		return nil
	}
	if port := os.Getenv("ROUTERLLM_PORT"); port != "" {
		if err := validatePort(port); err != nil {
			log.Printf("config error: %v", err)
			return nil
		}
		cfg.Port = port
	}

	applyOpencodeRelay(cfg)

	settings.Set(cfg.Settings)

	return cfg
}

// applyOpencodeRelay routes opencode providers through a local Bun TLS relay
// when OPENCODE_TLS_RELAY is enabled. The gateway scores clients at the
// transport level — from datacenter IPs only Bun-family clients pass its
// structural gate — so the provider base_url is rewritten to the relay while
// the original origin becomes the relay's upstream. Any relay failure falls
// back to the untouched base_url (direct connection); request handling never
// blocks on the relay.
func applyOpencodeRelay(cfg *Config) {
	if !relay.Enabled() {
		return
	}
	for i := range cfg.Providers {
		pc := &cfg.Providers[i]
		if pc.Style != "opencode" || pc.Disabled {
			continue
		}
		base, err := relay.Default().Ensure(context.Background(), pc.BaseURL)
		if err != nil {
			log.Printf("opencode tls relay unavailable (provider %q): %v — falling back to direct", pc.Name, err)
			continue
		}
		log.Printf("opencode provider %q routed through tls relay %s", pc.Name, base)
		pc.BaseURL = base
	}
}

// validatePort accepts an empty string (caller applies the default) and any
// integer in the TCP range. Port 0 is rejected on purpose: it binds a random
// port and silently breaks every client pointing at the configured one.
func validatePort(port string) error {
	if port == "" {
		return nil
	}

	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("invalid port %q: must be an integer between 1 and 65535", port)
	}

	return nil
}

func newTransport(s settings.Transport) *http.Transport {
	return &http.Transport{
		MaxIdleConns:          s.MaxIdleConns,
		MaxIdleConnsPerHost:   s.MaxIdleConnsPerHost,
		IdleConnTimeout:       s.IdleConnTimeout,
		ResponseHeaderTimeout: s.ResponseHeaderTimeout,
		TLSHandshakeTimeout:   s.TLSHandshakeTimeout,
	}
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
