package config

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"routerllm/internal/alysis"
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
	Port                 string         `yaml:"port,omitempty"`
	Cooldown             string         `yaml:"cooldown,omitempty"`
	ForceStream          bool           `yaml:"force_stream,omitempty"`
	ForwardClientHeaders *bool          `yaml:"forward_client_headers,omitempty"`
	AllowClientHeaders   []string       `yaml:"allow_client_headers,omitempty"`
	SystemPromptFile     string         `yaml:"system_prompt_file,omitempty"`
	Providers            []yamlProvider `yaml:"providers"`
	Routes               []model.Rule   `yaml:"routes,omitempty"`
}

type yamlProvider struct {
	Name           string            `yaml:"name"`
	Style          string            `yaml:"style,omitempty"`
	BaseURL        string            `yaml:"base_url,omitempty"`
	APIKey         stringOrList      `yaml:"api_key,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`
	AuthMode       string            `yaml:"auth_mode,omitempty"`
	Share          string            `yaml:"share,omitempty"`
	Query          string            `yaml:"query,omitempty"`
	ReasoningStyle string            `yaml:"reasoning_style,omitempty"`
	Disabled       bool              `yaml:"disabled,omitempty"`
}

func LoadFile(path string) (*Config, error) {
	return loadYAML(path)
}

func loadYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return parseBytesAt(data, path)
}

// ParseBytes decodes an in-memory config document. Callers that need the
// content hash of exactly what was parsed read the file once and use this. A
// relative system_prompt_file resolves against the directory of the served
// config file (ROUTERLLM_CONFIG_FILE), matching LoadFile.
func ParseBytes(data []byte) (*Config, error) {
	return parseBytesAt(data, ConfigPath())
}

// ValidateBytes parses a document exactly like LoadFile does, resolving
// referenced files (system prompt, account stores) against the served config
// path. The admin editor rejects a mutation with this before the bytes reach
// disk, so a rejected edit can never leave an unloadable file behind.
func ValidateBytes(data []byte) error {
	_, err := parseBytesAt(data, ConfigPath())

	return err
}

func parseBytesAt(data []byte, configPath string) (*Config, error) {
	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		return nil, err
	}

	return yamlToConfig(&yc, configPath)
}

func yamlToConfig(yc *yamlConfig, configPath string) (*Config, error) {
	if err := validateConfig(yc); err != nil {
		return nil, err
	}

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

	systemPrompt, err := loadSystemPrompt(yc.SystemPromptFile, configPath)
	if err != nil {
		return nil, err
	}

	var providers []ProviderConfig
	for _, yp := range yc.Providers {
		if yp.AuthMode == "" {
			yp.AuthMode = "bearer"
		}
		if yp.ReasoningStyle == "" {
			yp.ReasoningStyle = "openai"
		}

		keys := expandKeys(yp.APIKey)
		if yp.Style == "cline" && len(keys) == 0 && !yp.Disabled {
			store, err := cline.LoadAccountStore(cline.DefaultAccountsPath())
			if err != nil {
				return nil, fmt.Errorf("provider %q: %w", yp.Name, err)
			}
			keys = store.RefreshTokens()
			if len(keys) == 0 {
				return nil, fmt.Errorf("provider %q: no cline accounts found in %s — run `routerllm --cline-login`", yp.Name, store.Path())
			}
		}

		if yp.Style == "alysis" && len(keys) == 0 && !yp.Disabled {
			store, err := alysis.LoadAccountStore(alysis.DefaultAccountsPath())
			if err != nil {
				return nil, fmt.Errorf("provider %q: %w", yp.Name, err)
			}
			keys = store.GatewayKeys()
			if len(keys) == 0 {
				return nil, fmt.Errorf("provider %q: no alysis accounts found in %s — run `routerllm --alysis-login`", yp.Name, store.Path())
			}
		}

		p := ProviderConfig{
			Name:           yp.Name,
			BaseURL:        strings.TrimRight(yp.BaseURL, "/"),
			Style:          yp.Style,
			AuthMode:       yp.AuthMode,
			ShareKeys:      yp.Share,
			Query:          yp.Query,
			ReasoningStyle: yp.ReasoningStyle,
			Headers:        yp.Headers,
			Keys:           keys,
			Disabled:       yp.Disabled,
		}

		if p.Headers == nil {
			p.Headers = make(map[string]string)
		}
		p.BaseURL = strings.TrimSuffix(p.BaseURL, "/v1")
		providers = append(providers, p)
	}

	client := &http.Client{Transport: newTransport()}
	forwardClientHeaders := true
	if yc.ForwardClientHeaders != nil {
		forwardClientHeaders = *yc.ForwardClientHeaders
	}

	return &Config{
		Port:                 port,
		Cooldown:             cooldown,
		ForceStream:          yc.ForceStream,
		ForwardClientHeaders: forwardClientHeaders,
		AllowClientHeaders:   yc.AllowClientHeaders,
		SystemPrompt:         systemPrompt,
		Providers:            providers,
		Client:               client,
		Routes:               yc.Routes,
	}, nil
}

func loadSystemPrompt(path, configPath string) (string, error) {
	if path == "" {
		return "", nil
	}

	resolved := path
	if !filepath.IsAbs(resolved) && configPath != "" {
		resolved = filepath.Join(filepath.Dir(configPath), resolved)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read system_prompt_file %q: %w", path, err)
	}

	return strings.TrimSpace(string(data)), nil
}

func validateConfig(yc *yamlConfig) error {
	if err := validatePort(yc.Port); err != nil {
		return err
	}

	if len(yc.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}

	seenProviders := make(map[string]bool, len(yc.Providers))
	for i, yp := range yc.Providers {
		if err := validateProvider(yp, i, seenProviders); err != nil {
			return err
		}
	}

	if len(yc.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}

	seenModels := make(map[string]bool, len(yc.Routes))
	for _, rule := range yc.Routes {
		if rule.ModelID == "" {
			return fmt.Errorf("route has empty model name")
		}
		if seenModels[rule.ModelID] {
			return fmt.Errorf("duplicate route model_id %q", rule.ModelID)
		}
		seenModels[rule.ModelID] = true

		if len(rule.Routes) == 0 {
			return fmt.Errorf("route %q has no upstream routes", rule.ModelID)
		}
		for _, spec := range rule.Routes {
			if spec.Provider == "" {
				return fmt.Errorf("route %q has a spec with empty provider", rule.ModelID)
			}
			if !seenProviders[spec.Provider] {
				return fmt.Errorf("route %q references unknown provider %q", rule.ModelID, spec.Provider)
			}
			if spec.Model == "" {
				return fmt.Errorf("route %q provider %q has empty upstream model", rule.ModelID, spec.Provider)
			}
		}
	}

	return nil
}

func validateProvider(yp yamlProvider, index int, seen map[string]bool) error {
	if yp.Name == "" {
		return fmt.Errorf("provider at index %d has empty name", index)
	}
	if seen[yp.Name] {
		return fmt.Errorf("duplicate provider name %q", yp.Name)
	}
	seen[yp.Name] = true

	if yp.BaseURL == "" {
		return fmt.Errorf("provider %q has empty base_url", yp.Name)
	}

	switch yp.Style {
	case "openai", "anthropic", "cline", "google", "alysis":
	case "":
		return fmt.Errorf("provider %q: style is required (openai, anthropic, cline, google, or alysis)", yp.Name)
	default:
		return fmt.Errorf("provider %q: unsupported style %q (must be openai, anthropic, cline, google, or alysis)", yp.Name, yp.Style)
	}

	switch yp.AuthMode {
	case "bearer", "x-api-key", "both", "":
	default:
		return fmt.Errorf("provider %q: unsupported auth_mode %q (must be bearer, x-api-key, or both)", yp.Name, yp.AuthMode)
	}

	switch yp.ReasoningStyle {
	case "openai", "openrouter", "qwen", "raw", "":
	default:
		return fmt.Errorf("provider %q: unsupported reasoning_style %q (must be openai, openrouter, qwen, or raw)", yp.Name, yp.ReasoningStyle)
	}

	if len(yp.APIKey) == 0 && yp.Style != "cline" && yp.Style != "alysis" && !yp.Disabled {
		return fmt.Errorf("provider %q: api_key is required", yp.Name)
	}

	if !yp.Disabled {
		keys := expandKeys(yp.APIKey)
		for _, k := range keys {
			if strings.HasPrefix(k, "${") && strings.HasSuffix(k, "}") {
				return fmt.Errorf("provider %q: environment variable %s is not set", yp.Name, k)
			}
		}
		if len(keys) == 0 && yp.Style != "cline" && yp.Style != "alysis" {
			return fmt.Errorf("provider %q: api_key expanded to zero keys (is the environment variable empty?)", yp.Name)
		}
	}

	return nil
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
