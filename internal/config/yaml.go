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
	"routerllm/internal/codex"
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
	DedupeTools          bool           `yaml:"dedupe_tools,omitempty"`
	ClampToolSchemas     bool           `yaml:"clamp_tool_schemas,omitempty"`
	ToolSchemaMaxDepth   int            `yaml:"tool_schema_max_depth,omitempty"`
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

const (
	defaultPort     = "1765"
	defaultCooldown = 60 * time.Second

	// DefaultToolSchemaMaxDepth is the depth budget applied to outbound tool
	// schemas when clamp_tool_schemas is on and tool_schema_max_depth is unset.
	// It matches the proxy-side default and stays below the gateways' own limit
	// of 10 because the counters disagree at the edges.
	DefaultToolSchemaMaxDepth = 8
)

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
		port = defaultPort
	}

	cooldown := defaultCooldown
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
		if !yp.Disabled && len(keys) == 0 {
			storeKeys, path, ok, err := accountStyleKeys(yp.Style)
			if err != nil {
				return nil, fmt.Errorf("provider %q: %w", yp.Name, err)
			}
			if ok {
				if len(storeKeys) == 0 {
					return nil, fmt.Errorf("provider %q: no %s accounts found in %s — run `routerllm --%s-login`", yp.Name, yp.Style, path, yp.Style)
				}
				keys = storeKeys
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

	toolSchemaMaxDepth := yc.ToolSchemaMaxDepth
	if toolSchemaMaxDepth <= 0 {
		toolSchemaMaxDepth = DefaultToolSchemaMaxDepth
	}

	return &Config{
		Port:                 port,
		Cooldown:             cooldown,
		ForceStream:          yc.ForceStream,
		DedupeTools:          yc.DedupeTools,
		ClampToolSchemas:     yc.ClampToolSchemas,
		ToolSchemaMaxDepth:   toolSchemaMaxDepth,
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

	seenProviders := make(map[string]string, len(yc.Providers))
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
			if _, ok := seenProviders[spec.Provider]; !ok {
				return fmt.Errorf("route %q references unknown provider %q", rule.ModelID, spec.Provider)
			}
			if spec.Model == "" {
				return fmt.Errorf("route %q provider %q has empty upstream model", rule.ModelID, spec.Provider)
			}

			switch spec.StyleCall {
			case "chat", "responses", "messages", "":
			default:
				return fmt.Errorf("route %q provider %q: unsupported stylecall %q (must be chat, responses, or messages)", rule.ModelID, spec.Provider, spec.StyleCall)
			}

			if spec.StyleCall != "" {
				style := seenProviders[spec.Provider]
				if style == "google" || style == "cline" {
					return fmt.Errorf("route %q provider %q: stylecall is not supported on %s-style providers", rule.ModelID, spec.Provider, style)
				}
			}
		}
	}

	return nil
}

func validateProvider(yp yamlProvider, index int, seen map[string]string) error {
	if yp.Name == "" {
		return fmt.Errorf("provider at index %d has empty name", index)
	}
	if _, ok := seen[yp.Name]; ok {
		return fmt.Errorf("duplicate provider name %q", yp.Name)
	}
	seen[yp.Name] = yp.Style

	if yp.BaseURL == "" {
		return fmt.Errorf("provider %q has empty base_url", yp.Name)
	}

	switch yp.Style {
	case "openai", "anthropic", "cline", "google", "alysis", "codex", "opencode":
	case "":
		return fmt.Errorf("provider %q: style is required (openai, anthropic, cline, google, alysis, codex, or opencode)", yp.Name)
	default:
		return fmt.Errorf("provider %q: unsupported style %q (must be openai, anthropic, cline, google, alysis, codex, or opencode)", yp.Name, yp.Style)
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

	if len(yp.APIKey) == 0 && !isAccountStyle(yp.Style) && !yp.Disabled {
		return fmt.Errorf("provider %q: api_key is required", yp.Name)
	}

	if !yp.Disabled {
		keys := expandKeys(yp.APIKey)
		for _, k := range keys {
			if strings.HasPrefix(k, "${") && strings.HasSuffix(k, "}") {
				return fmt.Errorf("provider %q: environment variable %s is not set", yp.Name, k)
			}
		}
		if len(keys) == 0 && !isAccountStyle(yp.Style) {
			return fmt.Errorf("provider %q: api_key expanded to zero keys (is the environment variable empty?)", yp.Name)
		}
	}

	return nil
}

// isAccountStyle reports styles whose credentials come from a login-managed
// account store instead of api_key.
func isAccountStyle(style string) bool {
	return style == "cline" || style == "alysis" || style == "codex"
}

// accountStyleKeys loads the credentials of an account-backed style's store,
// returning the store path for error messages. ok is false when the style has
// no account store.
func accountStyleKeys(style string) (keys []string, path string, ok bool, err error) {
	switch style {
	case "cline":
		store, err := cline.LoadAccountStore(cline.DefaultAccountsPath())
		if err != nil {
			return nil, "", true, err
		}
		return store.RefreshTokens(), store.Path(), true, nil
	case "alysis":
		store, err := alysis.LoadAccountStore(alysis.DefaultAccountsPath())
		if err != nil {
			return nil, "", true, err
		}
		return store.GatewayKeys(), store.Path(), true, nil
	case "codex":
		store, err := codex.LoadAccountStore(codex.DefaultAccountsPath())
		if err != nil {
			return nil, "", true, err
		}
		return store.RefreshTokens(), store.Path(), true, nil
	}
	return nil, "", false, nil
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
