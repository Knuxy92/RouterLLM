package services

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"routerllm/internal/kilo"
)

// Kilo retry knobs mirror the Kilo CLI's own session retry
// (packages/opencode/src/session/retry.ts in kilo-org/kilocode):
// RETRY_INITIAL_DELAY=2000, RETRY_BACKOFF_FACTOR=2, RETRY_JITTER_FACTOR=0.25,
// RETRY_MAX_DELAY_NO_HEADERS=30_000. The attempt count is ours — the CLI
// defaults to 5, the operator asked for 10.
const kiloRetryAttempts = 10
const kiloRetryCap = 30 * time.Second

// kiloRetryBase is the first exponential delay (2s, like the CLI). A var so
// tests can shrink it — the same hook pattern as kilo.Client.pollInterval.
var kiloRetryBase = 2 * time.Second

// kiloRetryDelay picks the wait before the next kilo attempt the way the
// official CLI does: the gateway's Retry-After-MS / Retry-After headers when
// it sent one (the gateway is not guaranteed to send them), else exponential
// backoff — 2s, 4s, 8s, ... — with up to 25% jitter, capped at 30s. The CLI
// accepts any parseable header value, including 0 (retry immediately).
func kiloRetryDelay(h http.Header, attempt int) time.Duration {
	if h != nil {
		if ms := h.Get("Retry-After-MS"); ms != "" {
			if v, err := strconv.ParseFloat(ms, 64); err == nil {
				return clampDelay(time.Duration(v * float64(time.Millisecond)))
			}
		}
		if s := h.Get("Retry-After"); s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				return clampDelay(time.Duration(math.Ceil(v*1000) * float64(time.Millisecond)))
			}
			if t, err := http.ParseTime(s); err == nil && time.Until(t) > 0 {
				return clampDelay(time.Until(t))
			}
		}
	}
	return kiloExponentialDelay(attempt)
}

// clampDelay keeps a header-derived wait usable: never negative, never past
// the 30s cap (the CLI caps at the 32-bit timer limit; a long-lived proxy
// serving many clients wants the tighter bound).
func clampDelay(d time.Duration) time.Duration {
	return min(max(d, 0), kiloRetryCap)
}

// kiloExponentialDelay mirrors the CLI's no-headers path: base · 2^(attempt-1)
// plus up to 25% jitter, capped at kiloRetryCap.
func kiloExponentialDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := kiloRetryBase << (attempt - 1)
	return min(base+time.Duration(float64(base)*0.25*rand.Float64()), kiloRetryCap)
}

// kiloOrgMissOnce guards the store-miss warning so a mis-keyed provider (a
// yaml api_key override the store never saw, say) logs once per key instead
// of once per request.
var kiloOrgMissOnce sync.Map

// kiloOrg resolves the per-account organization id for a gateway key. Kilo
// tokens are long-lived and never rotate, so a direct store lookup is stable
// across requests; no sticky cache is needed. An account without an
// organization legitimately omits the header; a key the store has never seen
// gets a one-time warning.
func (p *Proxy) kiloOrg(token string) string {
	store, err := kilo.LoadAccountStore(kilo.DefaultAccountsPath())
	if err != nil {
		p.log.Printf("kilo accounts unavailable: %v", err)
		return ""
	}

	org, found := store.OrganizationID(token)
	if !found {
		if _, dup := kiloOrgMissOnce.LoadOrStore(token, true); !dup {
			p.log.Printf("kilo key %s is not in the accounts store (yaml api_key override?) — %s omitted", maskKey(token), kilo.OrgHeader)
		}
	}

	return org
}
