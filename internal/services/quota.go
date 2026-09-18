package services

import (
	"net/http"
	"strconv"
	"time"
)

const quotaWarnPercent = 80

// QuotaWindow is one Codex rate-limit window as advertised on upstream
// response headers (X-Codex-Primary-* / X-Codex-Secondary-*).
type QuotaWindow struct {
	UsedPercent   int   `json:"used_percent"`
	WindowMinutes int   `json:"window_minutes"`
	ResetAt       int64 `json:"reset_at"`
}

// QuotaSnapshot is the latest Codex rate-limit state observed for one provider
// key. The upstream repeats these headers on every response, so the snapshot
// stays current without any extra request.
type QuotaSnapshot struct {
	Primary   *QuotaWindow `json:"primary,omitempty"`
	Secondary *QuotaWindow `json:"secondary,omitempty"`
	PlanType  string       `json:"plan_type,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func parseQuotaHeaders(h http.Header) (QuotaSnapshot, bool) {
	if h == nil {
		return QuotaSnapshot{}, false
	}

	primary, hasPrimary := parseQuotaWindow(h, "X-Codex-Primary")
	secondary, hasSecondary := parseQuotaWindow(h, "X-Codex-Secondary")
	planType := h.Get("X-Codex-Plan-Type")
	if !hasPrimary && !hasSecondary && planType == "" {
		return QuotaSnapshot{}, false
	}

	snapshot := QuotaSnapshot{PlanType: planType, UpdatedAt: time.Now()}
	if hasPrimary {
		snapshot.Primary = &primary
	}
	// A zero-length secondary window is how the upstream says "no second
	// window"; it would only add a meaningless 0% row to the console.
	if hasSecondary && secondary.WindowMinutes > 0 {
		snapshot.Secondary = &secondary
	}

	return snapshot, true
}

// parseQuotaWindow reads one window's headers. A window needs at least a used
// percent; the window length and reset time are optional extras.
func parseQuotaWindow(h http.Header, prefix string) (QuotaWindow, bool) {
	raw := h.Get(prefix + "-Used-Percent")
	if raw == "" {
		return QuotaWindow{}, false
	}

	percent, err := strconv.Atoi(raw)
	if err != nil {
		return QuotaWindow{}, false
	}

	window := QuotaWindow{UsedPercent: percent}
	if minutes, err := strconv.Atoi(h.Get(prefix + "-Window-Minutes")); err == nil {
		window.WindowMinutes = minutes
	}
	if resetAt, err := strconv.ParseInt(h.Get(prefix+"-Reset-At"), 10, 64); err == nil {
		window.ResetAt = resetAt
	}

	return window, true
}
