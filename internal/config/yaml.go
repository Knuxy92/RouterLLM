package config

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"routerllm/internal/cline"
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
	AutoModel        yamlAutoModel  `yaml:"auto_model,omitempty"`
	Providers        []yamlProvider `yaml:"providers"`
	Routes           []model.Rule   `yaml:"routes,omitempty"`
}

type yamlAutoModel struct {
	Enabled       bool   `yaml:"enabled"`
	BaseURL       string `yaml:"base_url,omitempty"`
	APIKey        string `yaml:"api_key,omitempty"`
	Model         string `yaml:"model,omitempty"`
	PromptFile    string `yaml:"prompt_file,omitempty"`
	SmallModel    string `yaml:"small_model,omitempty"`
	AnalysisModel string `yaml:"analysis_model,omitempty"`
	CodingModel   string `yaml:"coding_model,omitempty"`
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
		d, err := time.ParseDuration(yc.Cooldown)
		if err != nil {
			return nil, fmt.Errorf("invalid cooldown %q: %w", yc.Cooldown, err)
		}
		cooldown = d
	}

	var systemPrompt string
	if yc.SystemPromptFile != "" {
		data, err := os.ReadFile(yc.SystemPromptFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read system_prompt_file %q: %w", yc.SystemPromptFile, err)
		}
		systemPrompt = strings.TrimSpace(string(data))
	}

	var providers []ProviderConfig
	seenProviders := make(map[string]bool)

	for _, yp := range yc.Providers {
		if yp.Name == "" {
			return nil, fmt.Errorf("provider at index %d has empty name", len(providers))
		}
		if seenProviders[yp.Name] {
			return nil, fmt.Errorf("duplicate provider name %q", yp.Name)
		}
		seenProviders[yp.Name] = true

		if yp.BaseURL == "" {
			return nil, fmt.Errorf("provider %q has empty base_url", yp.Name)
		}

		switch yp.Style {
		case "openai", "anthropic", "cline":
		case "":
			return nil, fmt.Errorf("provider %q: style is required (openai, anthropic, or cline)", yp.Name)
		default:
			return nil, fmt.Errorf("provider %q: unsupported style %q (must be openai, anthropic, or cline)", yp.Name, yp.Style)
		}

		switch yp.AuthMode {
		case "bearer", "x-api-key", "both":
		case "":
			yp.AuthMode = "bearer"
		default:
			return nil, fmt.Errorf("provider %q: unsupported auth_mode %q (must be bearer, x-api-key, or both)", yp.Name, yp.AuthMode)
		}

		if len(yp.APIKey) == 0 && yp.Style != "cline" {
			return nil, fmt.Errorf("provider %q: api_key is required", yp.Name)
		}

		keys := expandKeys(yp.APIKey)
		for _, k := range keys {
			if strings.HasPrefix(k, "${") && strings.HasSuffix(k, "}") {
				return nil, fmt.Errorf("provider %q: environment variable %s is not set", yp.Name, k)
			}
		}

		if yp.Style == "cline" && len(keys) == 0 {
			store, err := cline.LoadAccountStore(cline.DefaultAccountsPath())
			if err != nil {
				return nil, fmt.Errorf("provider %q: %w", yp.Name, err)
			}
			keys = store.RefreshTokens()
			if len(keys) == 0 {
				return nil, fmt.Errorf("provider %q: no cline accounts found in %s — run `routerllm --cline-login`", yp.Name, store.Path())
			}
		}

		p := ProviderConfig{
			Name:      yp.Name,
			BaseURL:   strings.TrimRight(yp.BaseURL, "/"),
			Style:     yp.Style,
			AuthMode:  yp.AuthMode,
			ShareKeys: yp.Share,
			Query:     yp.Query,
			Headers:   yp.Headers,
			Keys:      keys,
		}

		if p.Headers == nil {
			p.Headers = make(map[string]string)
		}
		p.BaseURL = strings.TrimSuffix(p.BaseURL, "/v1")
		providers = append(providers, p)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("at least one provider is required")
	}

	autoModel, err := buildAutoModel(&yc.AutoModel)
	if err != nil {
		return nil, err
	}

	if len(yc.Routes) == 0 {
		return nil, fmt.Errorf("at least one route is required")
	}

	for _, rule := range yc.Routes {
		if rule.ModelID == "" {
			return nil, fmt.Errorf("route has empty model name")
		}
		if len(rule.Routes) == 0 {
			return nil, fmt.Errorf("route %q has no upstream routes", rule.ModelID)
		}
		for _, spec := range rule.Routes {
			if spec.Provider == "" {
				return nil, fmt.Errorf("route %q has a spec with empty provider", rule.ModelID)
			}
			if !seenProviders[spec.Provider] {
				return nil, fmt.Errorf("route %q references unknown provider %q", rule.ModelID, spec.Provider)
			}
			if spec.Model == "" {
				return nil, fmt.Errorf("route %q provider %q has empty upstream model", rule.ModelID, spec.Provider)
			}
		}
	}

	client := &http.Client{Transport: newTransport()}

	return &Config{
		Port:         port,
		Cooldown:     cooldown,
		ForceStream:  yc.ForceStream,
		SystemPrompt: systemPrompt,
		AutoModel:    autoModel,
		Providers:    providers,
		Client:       client,
		Routes:       yc.Routes,
	}, nil
}

func buildAutoModel(yam *yamlAutoModel) (AutoModelConfig, error) {
	if !yam.Enabled {
		return AutoModelConfig{}, nil
	}

	if yam.BaseURL == "" || yam.APIKey == "" || yam.Model == "" || yam.PromptFile == "" {
		return AutoModelConfig{}, fmt.Errorf("auto_model requires base_url, api_key, model, and prompt_file when enabled")
	}
	key := resolveEnv(yam.APIKey)
	if len(key) != 1 || strings.HasPrefix(key[0], "${") {
		return AutoModelConfig{}, fmt.Errorf("auto_model: environment variable %s is not set", yam.APIKey)
	}
	prompt, err := os.ReadFile(yam.PromptFile)
	if err != nil {
		return AutoModelConfig{}, fmt.Errorf("failed to read auto_model prompt_file %q: %w", yam.PromptFile, err)
	}
	if yam.SmallModel == "" || yam.AnalysisModel == "" || yam.CodingModel == "" {
		return AutoModelConfig{}, fmt.Errorf("auto_model requires small_model, analysis_model, and coding_model when enabled")
	}

	return AutoModelConfig{
		Enabled:       true,
		BaseURL:       strings.TrimSuffix(strings.TrimRight(yam.BaseURL, "/"), "/v1"),
		APIKey:        key[0],
		Model:         yam.Model,
		Prompt:        strings.TrimSpace(string(prompt)),
		SmallModel:    yam.SmallModel,
		AnalysisModel: yam.AnalysisModel,
		CodingModel:   yam.CodingModel,
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
