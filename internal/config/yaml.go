package config

import (
	"net/http"
	"os"
	"strings"
	"time"

	"routerllm/internal/model"

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
	Port             string         `yaml:"port,omitempty"`
	Cooldown         string         `yaml:"cooldown,omitempty"`
	ForceStream      bool           `yaml:"force_stream,omitempty"`
	SystemPromptFile string         `yaml:"system_prompt_file,omitempty"`
	Providers        []yamlProvider `yaml:"providers"`
	Routes           []model.Rule   `yaml:"routes,omitempty"`
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

	var systemPrompt string
	if yc.SystemPromptFile != "" {
		data, err := os.ReadFile(yc.SystemPromptFile)
		if err == nil {
			systemPrompt = strings.TrimSpace(string(data))
		}
	}

	var providers []ProviderConfig
	for _, yp := range yc.Providers {
		p := ProviderConfig{
			Name:      yp.Name,
			BaseURL:   strings.TrimRight(yp.BaseURL, "/"),
			Style:     yp.Style,
			AuthMode:  yp.AuthMode,
			ShareKeys: yp.Share,
			Query:     yp.Query,
			Headers:   yp.Headers,
		}
		
		if p.Headers == nil {
			p.Headers = make(map[string]string)
		}
		
		p.BaseURL = strings.TrimSuffix(p.BaseURL, "/v1")
		if len(yp.APIKey) > 0 {
			p.Keys = expandKeys(yp.APIKey)
		} else {
			p.Keys = []string{}
		}
		
		providers = append(providers, p)
	}

	client := &http.Client{Transport: newTransport()}

	return &Config{
		Port:          port,
		Cooldown:      cooldown,
		ForceStream:   yc.ForceStream,
		SystemPrompt:  systemPrompt,
		Providers:     providers,
		Client:        client,
		Routes:        yc.Routes,
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
