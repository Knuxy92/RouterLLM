package config

import (
	"net/http"
	"os"
	"strings"
	"time"

	"routerllm/internal/routing"

	"gopkg.in/yaml.v3"
)

type stringOrList []string

func (s *stringOrList) UnmarshalYAML(value *yaml.Node) error {
	var single string
	if err := value.Decode(&single); err == nil {
		*s = []string{single}
		return nil
	}
	return value.Decode((*[]string)(s))
}

type yamlConfig struct {
	Port      string         `yaml:"port,omitempty"`
	Cooldown  string         `yaml:"cooldown,omitempty"`
	Providers []yamlProvider `yaml:"providers"`
	Routes    []routing.Rule `yaml:"routes,omitempty"`
}

type yamlProvider struct {
	Name     string            `yaml:"name"`
	Style    string            `yaml:"style,omitempty"`
	BaseURL  string            `yaml:"base_url,omitempty"`
	APIKey   stringOrList      `yaml:"api_key,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty"`
	AuthMode string            `yaml:"auth_mode,omitempty"`
	Share    string            `yaml:"share,omitempty"`
	Query    string            `yaml:"query,omitempty"`
}

func loadYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		return nil, err
	}
	return yamlToConfig(&yc)
}

func yamlToConfig(yc *yamlConfig) (*Config, error) {
	port := yc.Port
	if port == "" {
		port = "1765"
	}
	cooldown := 60 * time.Second
	if yc.Cooldown != "" {
		if d, err := time.ParseDuration(yc.Cooldown); err == nil {
			cooldown = d
		}
	}

	var providers []ProviderConfig
	for _, yp := range yc.Providers {
		def := providerDefaults[yp.Name]
		p := ProviderConfig{
			Name: yp.Name,
		}
		if yp.Style != "" {
			p.Style = yp.Style
		} else {
			p.Style = def.Style
		}
		if yp.BaseURL != "" {
			p.BaseURL = yp.BaseURL
		} else {
			p.BaseURL = def.BaseURL
		}
		p.BaseURL = strings.TrimRight(p.BaseURL, "/")
		p.BaseURL = strings.TrimSuffix(p.BaseURL, "/v1")
		p.Headers = make(map[string]string)
		for k, v := range def.Headers {
			p.Headers[k] = v
		}
		for k, v := range yp.Headers {
			p.Headers[k] = v
		}
		if yp.AuthMode != "" {
			p.AuthMode = yp.AuthMode
		} else {
			p.AuthMode = def.AuthMode
		}
		if yp.Share != "" {
			p.ShareKeys = yp.Share
		} else {
			p.ShareKeys = def.ShareKeys
		}
		if yp.Query != "" {
			p.Query = yp.Query
		} else {
			p.Query = def.Query
		}
		if len(yp.APIKey) > 0 {
			p.Keys = expandKeys(yp.APIKey)
		} else {
			p.Keys = []string{}
		}
		providers = append(providers, p)
	}

	routes := yc.Routes
	if routes == nil {
		routes = routing.DefaultRules()
	}

	client := &http.Client{Transport: newTransport()}

	return &Config{
		Port:       port,
		Cooldown:   cooldown,
		Providers:  providers,
		Client:     client,
		Routes:     routes,
		RoutesFile: "",
	}, nil
}

func expandKeys(raw []string) []string {
	var out []string
	for _, s := range raw {
		keys := resolveEnv(s)
		out = append(out, keys...)
	}
	return out
}

func resolveEnv(s string) []string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		key := s[2 : len(s)-1]
		if v, ok := os.LookupEnv(key); ok {
			return splitList(v)
		}
	}
	return []string{s}
}

type providerDefault struct {
	Style     string
	BaseURL   string
	Headers   map[string]string
	AuthMode  string
	ShareKeys string
	Query     string
}

var providerDefaults = map[string]providerDefault{
	"agentrouter": {
		Style:    "openai",
		BaseURL:  "https://agentrouter.org",
		Headers:  stainlessHeaders(),
		AuthMode: "bearer",
	},
	"opencode": {
		Style:    "openai",
		BaseURL:  "https://opencode.ai/zen",
		Headers:  map[string]string{"Content-Type": "application/json"},
		AuthMode: "bearer",
	},
	"alibaba": {
		Style:    "openai",
		BaseURL:  "https://ws-1h0fygqzbmuhg6j1.ap-southeast-1.maas.aliyuncs.com/compatible-mode",
		Headers:  map[string]string{"Content-Type": "application/json"},
		AuthMode: "bearer",
	},
	"freemodel-api": {
		Style:     "openai",
		BaseURL:   "https://api.freemodel.dev",
		Headers:   freemodelHeaders(),
		AuthMode:  "both",
		ShareKeys: "freemodel",
		Query:     "?beta=true",
	},
	"freemodel-cc": {
		Style:     "anthropic",
		BaseURL:   "https://cc.freemodel.dev",
		Headers:   freemodelHeaders(),
		AuthMode:  "both",
		ShareKeys: "freemodel",
		Query:     "?beta=true",
	},
	"aerolink": {
		Style:    "anthropic",
		BaseURL:  "https://capi.aerolink.lat",
		Headers:  aerolinkHeaders(),
		AuthMode: "x-api-key",
	},
	"forge": {
		Style:    "openai",
		BaseURL:  "https://forge-gateway-api.fly.dev",
		Headers:  map[string]string{"Content-Type": "application/json"},
		AuthMode: "bearer",
	},
}
