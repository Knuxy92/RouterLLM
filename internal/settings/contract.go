// Contract values. These are not preferences: each one is a limit or identifier
// an upstream enforces, so exposing them in YAML would only let an operator
// break every request with no local way to tell why. They stay constants on
// purpose — change one only with evidence from the upstream.
package settings

import "time"

const (
	// ToolNameMaxLength is the longest function name OpenAI-compatible upstreams
	// accept. Longer names are rewritten to a 64-char hash suffix.
	ToolNameMaxLength = 64

	// OpenCodeMinMaxTokens is the OpenCode gateway's floor for any token-cap
	// field; smaller values are rejected with "The number must be `>= 16`".
	OpenCodeMinMaxTokens = 16

	// DefaultAnthropicMaxTokens applies when a chat body carries no max_tokens
	// and the leg speaks the Anthropic dialect, which requires the field.
	DefaultAnthropicMaxTokens = 4096

	// ThinkingHeadroom is subtracted from the token budget when an Anthropic
	// thinking block is present, because the budget counts toward the cap.
	ThinkingHeadroom = 1024

	// ManualDisableHorizonYears marks a manually disabled key as dead far beyond
	// any cooldown, so a registry rebuild can tell it apart from a transient one.
	ManualDisableHorizonYears = 100

	// QuotaWarnPercent is the used-percentage at which a Codex key is logged as
	// having crossed its warning threshold.
	QuotaWarnPercent = 80
)

// Device-flow and token-lifetime values shared by the account-auth styles. They
// mirror what each vendor's CLI does; the vendors validate them.
const (
	PollIntervalFloor = 5 * time.Second
	SlowDownBump      = 5 * time.Second
	TokenSkew         = time.Minute
	DefaultTokenTTL   = 3600
	DefaultDeviceTTL  = 900
	DeviceExpiryFallback = 300
	MaxPollDuration      = 15 * time.Minute
	MinPollInterval      = 5 * time.Second
	MaxClientNameLength  = 80
)
