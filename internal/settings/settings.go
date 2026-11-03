// Package settings holds every tunable the proxy reads at runtime, in one
// place. The YAML `settings:` block maps onto Settings; consumers read the
// live snapshot through Current instead of carrying the struct through their
// constructors.
//
// Settings must stay a leaf: stdlib only, no repo imports. config imports it to
// fill the struct from YAML, and telemetry/audit/services/admin read it — a
// reverse import would make every consumer depend on the provider packages
// config pulls in.
package settings

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Settings is the whole tunable surface. Every zero value here means "unset"
// only before Default is applied; after that a zero field is a validation
// error, so a typo in the YAML fails the load instead of silently disabling a
// limit.
type Settings struct {
	Limits    Limits    `yaml:"limits"`
	Retry     Retry     `yaml:"retry"`
	Telemetry Telemetry `yaml:"telemetry"`
	Admin     Admin     `yaml:"admin"`
	Audit     Audit     `yaml:"audit"`

	// Startup-only: changing these requires a restart because they are read
	// while building the transport, the HTTP server or the admin certificate.
	Transport Transport `yaml:"transport"`
	Server    Server    `yaml:"server"`
	AdminTLS  AdminTLS  `yaml:"admin_tls"`
}

// Limits bounds request and response payloads.
type Limits struct {
	RequestBodyBytes int  `yaml:"request_body_bytes"`
	ErrorBodyBytes   int  `yaml:"error_body_bytes"`
	BriefBodyBytes   int  `yaml:"brief_body_bytes"`
	UpstreamTools    int  `yaml:"upstream_tools"`
	SchemaMaxDepth   int  `yaml:"schema_max_depth"`
	Media            Media `yaml:"media"`
}

// Media bounds the outbound fetches that resolve media references.
type Media struct {
	ResolvedSizeBytes int `yaml:"resolved_size_bytes"`
	RefsPerRequest    int `yaml:"refs_per_request"`
	BytesPerRequest   int `yaml:"bytes_per_request"`
	Redirects         int `yaml:"redirects"`
}

// Retry bounds the per-key retry loop.
type Retry struct {
	Attempts      int `yaml:"attempts"`
	BackoffBaseMS int `yaml:"backoff_base_ms"`
}

// Telemetry bounds the in-memory event ring and the on-disk jsonl.
type Telemetry struct {
	RingCapacity        int           `yaml:"ring_capacity"`
	Retention           time.Duration `yaml:"retention"`
	FileBytes           int           `yaml:"file_bytes"`
	RewriteBytes        int           `yaml:"rewrite_bytes"`
	RotateRetryInterval time.Duration `yaml:"rotate_retry_interval"`
	BucketWidth         time.Duration `yaml:"bucket_width"`
	LineBytes           int           `yaml:"line_bytes"`
	FieldBytes          int           `yaml:"field_bytes"`
	ErrorBytes          int           `yaml:"error_bytes"`
	RespBodyBytes       int           `yaml:"resp_body_bytes"`
	TTFTReservoir       int           `yaml:"ttft_reservoir"`
	ModelSeries         int           `yaml:"model_series"`
	UsageTailBytes      int           `yaml:"usage_tail_bytes"`
	DefaultPerPage      int           `yaml:"default_per_page"`
}

// Admin bounds the console's session store and its provider-test panel.
type Admin struct {
	SessionTTL       time.Duration `yaml:"session_ttl"`
	ChallengeTTL     time.Duration `yaml:"challenge_ttl"`
	MaxSessions      int           `yaml:"max_sessions"`
	MaxChallenges    int           `yaml:"max_challenges"`
	JSONBodyBytes    int           `yaml:"json_body_bytes"`
	Test             AdminTest     `yaml:"test"`
	UI               AdminUI       `yaml:"ui"`
}

// AdminTest bounds one provider-test run.
type AdminTest struct {
	PromptBytes           int    `yaml:"prompt_bytes"`
	MaxTokens             int    `yaml:"max_tokens"`
	DefaultTokens         int    `yaml:"default_tokens"`
	TimeoutFloorSeconds   int    `yaml:"timeout_floor_seconds"`
	TimeoutCeilingSeconds int    `yaml:"timeout_ceiling_seconds"`
	DefaultTimeoutSeconds int    `yaml:"default_timeout_seconds"`
	DefaultPrompt         string `yaml:"default_prompt"`
}

// AdminUI is served to the embedded console bundle through /admin/api/config so
// the frontend stops hardcoding its poll cadence and page size.
type AdminUI struct {
	PollMS     int `yaml:"poll_ms"`
	LogPerPage int `yaml:"log_per_page"`
}

// Audit bounds the request audit trail written when ROUTERLLM_DEBUG_ADVANCED is
// on.
type Audit struct {
	BodyBytes   int `yaml:"body_bytes"`
	RenderBytes int `yaml:"render_bytes"`
}

// Transport configures the shared upstream HTTP transport.
type Transport struct {
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`
}

// Server configures the HTTP server and the background tickers.
type Server struct {
	ReadHeaderTimeout    time.Duration `yaml:"read_header_timeout"`
	IdleTimeout          time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout      time.Duration `yaml:"shutdown_timeout"`
	MetricsPruneInterval time.Duration `yaml:"metrics_prune_interval"`
	WatchInterval        time.Duration `yaml:"watch_interval"`
}

// AdminTLS configures the self-signed console certificate.
type AdminTLS struct {
	CertValidityDays int `yaml:"cert_validity_days"`
}

// Default returns the values the code used before they were configurable. Every
// consumer keeps behaving identically when the YAML has no settings block.
func Default() Settings {
	return Settings{
		Limits: Limits{
			RequestBodyBytes: 100 << 20,
			ErrorBodyBytes:   64 << 10,
			BriefBodyBytes:   500,
			UpstreamTools:    128,
			SchemaMaxDepth:   DefaultToolSchemaMaxDepth,
			Media: Media{
				ResolvedSizeBytes: 20 << 20,
				RefsPerRequest:    8,
				BytesPerRequest:   50 << 20,
				Redirects:         10,
			},
		},
		Retry: Retry{
			Attempts:      3,
			BackoffBaseMS: 100,
		},
		Telemetry: Telemetry{
			RingCapacity:        2000,
			Retention:           7 * 24 * time.Hour,
			FileBytes:           10 << 20,
			RewriteBytes:        5 << 20,
			RotateRetryInterval: time.Minute,
			BucketWidth:         5 * time.Minute,
			LineBytes:           1 << 20,
			FieldBytes:          512,
			ErrorBytes:          4 << 10,
			RespBodyBytes:       2 << 10,
			TTFTReservoir:       64,
			ModelSeries:         256,
			UsageTailBytes:      4 << 10,
			DefaultPerPage:      50,
		},
		Admin: Admin{
			SessionTTL:    12 * time.Hour,
			ChallengeTTL:  60 * time.Second,
			MaxSessions:   100,
			MaxChallenges: 1000,
			JSONBodyBytes: 1 << 20,
			Test: AdminTest{
				PromptBytes:           4000,
				MaxTokens:             8192,
				DefaultTokens:         256,
				TimeoutFloorSeconds:   5,
				TimeoutCeilingSeconds: 120,
				DefaultTimeoutSeconds: 20,
				DefaultPrompt:         "Say 'pong' and nothing else.",
			},
			UI: AdminUI{
				PollMS:     3000,
				LogPerPage: 20,
			},
		},
		Audit: Audit{
			BodyBytes:   10 << 20,
			RenderBytes: 64 << 10,
		},
		Transport: Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 600 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		},
		Server: Server{
			ReadHeaderTimeout:    10 * time.Second,
			IdleTimeout:          120 * time.Second,
			ShutdownTimeout:      10 * time.Second,
			MetricsPruneInterval: time.Hour,
			WatchInterval:        3 * time.Second,
		},
		AdminTLS: AdminTLS{
			CertValidityDays: 825,
		},
	}
}

// DefaultToolSchemaMaxDepth is the depth budget applied to outbound tool
// schemas when clamp_tool_schemas is on and schema_max_depth is unset. It stays
// below the gateways' own limit of 10 because the counters disagree at the
// edges (properties wrappers, $defs, tuple items).
const DefaultToolSchemaMaxDepth = 8

// Validate rejects a settings block that would disable a limit or contradict
// itself. A rejection leaves the previously applied settings serving, exactly
// like any other config validation error.
func (s Settings) Validate() error {
	positive := []struct {
		name  string
		value int
	}{
		{"limits.request_body_bytes", s.Limits.RequestBodyBytes},
		{"limits.error_body_bytes", s.Limits.ErrorBodyBytes},
		{"limits.brief_body_bytes", s.Limits.BriefBodyBytes},
		{"limits.upstream_tools", s.Limits.UpstreamTools},
		{"limits.schema_max_depth", s.Limits.SchemaMaxDepth},
		{"limits.media.resolved_size_bytes", s.Limits.Media.ResolvedSizeBytes},
		{"limits.media.refs_per_request", s.Limits.Media.RefsPerRequest},
		{"limits.media.bytes_per_request", s.Limits.Media.BytesPerRequest},
		{"limits.media.redirects", s.Limits.Media.Redirects},
		{"retry.attempts", s.Retry.Attempts},
		{"retry.backoff_base_ms", s.Retry.BackoffBaseMS},
		{"telemetry.ring_capacity", s.Telemetry.RingCapacity},
		{"telemetry.file_bytes", s.Telemetry.FileBytes},
		{"telemetry.rewrite_bytes", s.Telemetry.RewriteBytes},
		{"telemetry.line_bytes", s.Telemetry.LineBytes},
		{"telemetry.field_bytes", s.Telemetry.FieldBytes},
		{"telemetry.error_bytes", s.Telemetry.ErrorBytes},
		{"telemetry.resp_body_bytes", s.Telemetry.RespBodyBytes},
		{"telemetry.ttft_reservoir", s.Telemetry.TTFTReservoir},
		{"telemetry.model_series", s.Telemetry.ModelSeries},
		{"telemetry.usage_tail_bytes", s.Telemetry.UsageTailBytes},
		{"telemetry.default_per_page", s.Telemetry.DefaultPerPage},
		{"admin.max_sessions", s.Admin.MaxSessions},
		{"admin.max_challenges", s.Admin.MaxChallenges},
		{"admin.json_body_bytes", s.Admin.JSONBodyBytes},
		{"admin.test.prompt_bytes", s.Admin.Test.PromptBytes},
		{"admin.test.max_tokens", s.Admin.Test.MaxTokens},
		{"admin.test.default_tokens", s.Admin.Test.DefaultTokens},
		{"admin.test.timeout_floor_seconds", s.Admin.Test.TimeoutFloorSeconds},
		{"admin.test.timeout_ceiling_seconds", s.Admin.Test.TimeoutCeilingSeconds},
		{"admin.test.default_timeout_seconds", s.Admin.Test.DefaultTimeoutSeconds},
		{"admin.ui.poll_ms", s.Admin.UI.PollMS},
		{"admin.ui.log_per_page", s.Admin.UI.LogPerPage},
		{"audit.body_bytes", s.Audit.BodyBytes},
		{"audit.render_bytes", s.Audit.RenderBytes},
		{"transport.max_idle_conns", s.Transport.MaxIdleConns},
		{"transport.max_idle_conns_per_host", s.Transport.MaxIdleConnsPerHost},
		{"admin_tls.cert_validity_days", s.AdminTLS.CertValidityDays},
	}
	for _, field := range positive {
		if field.value <= 0 {
			return fmt.Errorf("settings.%s must be greater than 0, got %d", field.name, field.value)
		}
	}

	positiveDuration := []struct {
		name  string
		value time.Duration
	}{
		{"telemetry.retention", s.Telemetry.Retention},
		{"telemetry.rotate_retry_interval", s.Telemetry.RotateRetryInterval},
		{"telemetry.bucket_width", s.Telemetry.BucketWidth},
		{"admin.session_ttl", s.Admin.SessionTTL},
		{"admin.challenge_ttl", s.Admin.ChallengeTTL},
		{"transport.idle_conn_timeout", s.Transport.IdleConnTimeout},
		{"transport.response_header_timeout", s.Transport.ResponseHeaderTimeout},
		{"transport.tls_handshake_timeout", s.Transport.TLSHandshakeTimeout},
		{"server.read_header_timeout", s.Server.ReadHeaderTimeout},
		{"server.idle_timeout", s.Server.IdleTimeout},
		{"server.shutdown_timeout", s.Server.ShutdownTimeout},
		{"server.metrics_prune_interval", s.Server.MetricsPruneInterval},
		{"server.watch_interval", s.Server.WatchInterval},
	}
	for _, field := range positiveDuration {
		if field.value <= 0 {
			return fmt.Errorf("settings.%s must be greater than 0, got %s", field.name, field.value)
		}
	}

	if s.Limits.UpstreamTools > 128 {
		return fmt.Errorf("settings.limits.upstream_tools must be at most 128, got %d", s.Limits.UpstreamTools)
	}
	if s.Telemetry.RewriteBytes >= s.Telemetry.FileBytes {
		return fmt.Errorf("settings.telemetry.rewrite_bytes (%d) must be smaller than file_bytes (%d)",
			s.Telemetry.RewriteBytes, s.Telemetry.FileBytes)
	}
	if s.Admin.Test.TimeoutFloorSeconds >= s.Admin.Test.TimeoutCeilingSeconds {
		return fmt.Errorf("settings.admin.test.timeout_floor_seconds (%d) must be smaller than timeout_ceiling_seconds (%d)",
			s.Admin.Test.TimeoutFloorSeconds, s.Admin.Test.TimeoutCeilingSeconds)
	}
	if s.Admin.Test.DefaultTimeoutSeconds < s.Admin.Test.TimeoutFloorSeconds ||
		s.Admin.Test.DefaultTimeoutSeconds > s.Admin.Test.TimeoutCeilingSeconds {
		return fmt.Errorf("settings.admin.test.default_timeout_seconds (%d) must be between %d and %d",
			s.Admin.Test.DefaultTimeoutSeconds, s.Admin.Test.TimeoutFloorSeconds, s.Admin.Test.TimeoutCeilingSeconds)
	}
	if s.Admin.Test.DefaultTokens > s.Admin.Test.MaxTokens {
		return fmt.Errorf("settings.admin.test.default_tokens (%d) must not exceed max_tokens (%d)",
			s.Admin.Test.DefaultTokens, s.Admin.Test.MaxTokens)
	}

	return nil
}

// Normalize fills the derived fields. Call it once after YAML mapping and
// before Validate.
func (s *Settings) Normalize() {
	// rewrite_bytes is a fraction of file_bytes, not an independent knob: a
	// budget at or above the rotation threshold would rewrite the whole ring on
	// every rotation. Deriving it also means setting only file_bytes moves both.
	if s.Telemetry.RewriteBytes <= 0 || s.Telemetry.RewriteBytes >= s.Telemetry.FileBytes {
		s.Telemetry.RewriteBytes = s.Telemetry.FileBytes / 2
	}
}

var current atomic.Pointer[Settings]

func init() {
	fallback := Default()
	current.Store(&fallback)
}

// Current returns the live snapshot. The pointer is immutable once published, so
// callers can read fields without copying; a reload swaps the pointer and the
// request in flight keeps the values it started with.
func Current() *Settings {
	return current.Load()
}

// Set publishes a new snapshot. Callers must have validated it first.
func Set(s Settings) {
	snapshot := s
	current.Store(&snapshot)
}
