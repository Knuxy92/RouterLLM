// Package quota reads the Codex rate-limit headers the gateway repeats on
// every upstream response and turns them into a snapshot the console can show
// per provider key. It holds no proxy state — the Proxy owns the store and
// calls ParseHeaders on each response.
package quota

import (
	"net/http"
	"strconv"
	"time"
)

// WarnPercent is the used-percentage threshold at which the proxy logs a
// one-line warning as a key crosses it.
const WarnPercent = 80

// Window is one Codex rate-limit window as advertised on upstream
// response headers (X-Codex-Primary-* / X-Codex-Secondary-*).
type Window struct {
	UsedPercent   int   `json:"used_percent"`
	WindowMinutes int   `json:"window_minutes"`
	ResetAt       int64 `json:"reset_at"`
}

// Snapshot is the latest Codex rate-limit state observed for one provider
// key. The upstream repeats these headers on every response, so the snapshot
// stays current without any extra request.
type Snapshot struct {
	Primary   *Window   `json:"primary,omitempty"`
	Secondary *Window   `json:"secondary,omitempty"`
	PlanType  string    `json:"plan_type,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ParseHeaders(h http.Header) (Snapshot, bool) {
	if h == nil {
		return Snapshot{}, false
	}

	primary, hasPrimary := parseQuotaWindow(h, "X-Codex-Primary")
	secondary, hasSecondary := parseQuotaWindow(h, "X-Codex-Secondary")
	planType := h.Get("X-Codex-Plan-Type")
	if !hasPrimary && !hasSecondary && planType == "" {
		return Snapshot{}, false
	}

	snapshot := Snapshot{PlanType: planType, UpdatedAt: time.Now()}
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
func parseQuotaWindow(h http.Header, prefix string) (Window, bool) {
	raw := h.Get(prefix + "-Used-Percent")
	if raw == "" {
		return Window{}, false
	}

	percent, err := strconv.Atoi(raw)
	if err != nil {
		return Window{}, false
	}

	window := Window{UsedPercent: percent}
	if minutes, err := strconv.Atoi(h.Get(prefix + "-Window-Minutes")); err == nil {
		window.WindowMinutes = minutes
	}
	if resetAt, err := strconv.ParseInt(h.Get(prefix+"-Reset-At"), 10, 64); err == nil {
		window.ResetAt = resetAt
	}

	return window, true
}
