package config

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"routerllm/internal/model"
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
	Disabled  bool
}

type Config struct {
	Port                 string
	Cooldown             time.Duration
	ForceStream          bool
	ForwardClientHeaders bool
	AllowClientHeaders   []string
	SystemPrompt         string
	Providers            []ProviderConfig
	Client               *http.Client
	Routes               []model.Rule
}

func Load() *Config {
	_ = util.LoadDotenv(".env")

	cfg, err := loadYAML(ConfigPath())
	if err != nil {
		log.Printf("config error: %v", err)
		return nil
	}
	if port := os.Getenv("ROUTERLLM_PORT"); port != "" {
		cfg.Port = port
	}

	return cfg
}

func newTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 600 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
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
