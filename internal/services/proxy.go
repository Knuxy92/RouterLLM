package services

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"routerllm/internal/cline"
	"routerllm/internal/codex"
	"routerllm/internal/opencode"
	"routerllm/internal/provider"
	"routerllm/internal/quota"
	"routerllm/internal/telemetry"
	"routerllm/internal/workbuddy"
)

var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"content-length":      true,
	"host":                true,
	"keep-alive":          true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// clientCredentialHeaders never reach an upstream, even when client header
// forwarding is on and the allowlist names them: the router picks the upstream
// credential, and a client's own credentials or session cookies must not ride
// along to a third-party provider.
var clientCredentialHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	googleAPIKeyHeader:    true,
	"cookie":              true,
	"cookie2":             true,
	"proxy-authorization": true,
	"accept-encoding":     true,
}

type Proxy struct {
	registry             atomic.Pointer[provider.Registry]
	client               *http.Client
	log                  *log.Logger
	debug                bool
	advancedDebug        bool
	forceStream          atomic.Bool
	forceStreamUsage     bool
	dedupeTools          atomic.Bool
	clampTools           atomic.Bool
	toolSchemaMaxDepth   atomic.Int64
	forwardClientHeaders atomic.Bool
	allowClientHeaders   atomic.Pointer[[]string]
	systemPrompt         atomic.Pointer[string]
	clineMu              sync.Mutex
	clineManagers        map[string]*cline.Manager
	codexMu              sync.Mutex
	codexManager         *codex.Manager
	opencodeMu           sync.Mutex
	opencodeManagers     map[string]*opencode.Manager
	opencodeBinding      map[string]opencodeBindingKey
	workbuddyMu          sync.Mutex
	workbuddyManagers    map[string]*workbuddy.Manager
	workbuddyBinding     map[string]workbuddyBindingKey
	quotaMu              sync.Mutex
	quotas               map[string]map[string]quota.Snapshot
	telemetry            atomic.Pointer[telemetry.Store]
}

// debug controls the per-request routing trace (which model, which provider
// served it). advancedDebug additionally logs response bodies. Failure lines —
// dead keys, retries, exhausted providers — are always logged regardless, since
// they are what an operator needs when a provider breaks.
func NewProxy(reg *provider.Registry, client *http.Client, log *log.Logger, debug bool, advancedDebug bool, forceStream bool, forwardClientHeaders bool, allowClientHeaders []string, systemPrompt string) *Proxy {
	p := &Proxy{client: client, log: log, debug: debug, advancedDebug: advancedDebug, forceStreamUsage: os.Getenv("ROUTERLLM_TELEMETRY_USAGE") != "off", clineManagers: make(map[string]*cline.Manager), opencodeManagers: make(map[string]*opencode.Manager), workbuddyManagers: make(map[string]*workbuddy.Manager), quotas: make(map[string]map[string]quota.Snapshot)}
	p.forceStream.Store(forceStream)
	p.forwardClientHeaders.Store(forwardClientHeaders)
	p.storeAllowClientHeaders(allowClientHeaders)
	p.telemetry.Store(telemetry.NewMemStore())
	p.registry.Store(reg)
	p.systemPrompt.Store(&systemPrompt)

	return p
}

// SetTelemetry attaches the request-event sink. Passing nil attaches an
// in-memory no-persist store so recording stays nil-safe everywhere.
func (p *Proxy) SetTelemetry(s *telemetry.Store) {
	if s == nil {
		s = telemetry.NewMemStore()
	}
	p.telemetry.Store(s)
}

// recordTelemetry fills the common fields and hands the event to the store.
func (p *Proxy) recordTelemetry(e telemetry.Event) {
	p.telemetry.Load().Record(e)
}

// RecordTelemetry hands a finished event to the store. Handlers outside
// services (e.g. /v1/messages) use it to close out a RequestTrace they started
// with WithRequestTrace.
func (p *Proxy) RecordTelemetry(e telemetry.Event) {
	p.recordTelemetry(e)
}

func (p *Proxy) Registry() *provider.Registry {
	return p.registry.Load()
}

// Apply swaps in a rebuilt registry. Cline managers are keyed by provider
// base URL, so any manager whose URL is no longer configured is dropped here —
// otherwise it would retain its account store and refresh-token cache for the
// process lifetime.
func (p *Proxy) Apply(reg *provider.Registry, systemPrompt string) {
	p.registry.Store(reg)
	p.systemPrompt.Store(&systemPrompt)
	p.pruneClineManagers(reg)
	p.pruneCodexManager(reg)
	p.pruneOpencodeManagers(reg)
	p.pruneWorkbuddyManagers(reg)
	p.pruneQuotas(reg)
}

// FlushAccounts retries persisting account-file updates that could not be
// written when they happened — a locked file, a read-only mount — so an
// orderly shutdown never leaves an account file behind a rotated refresh
// token and forces a re-login on the next start. Best-effort: failures are
// logged and shutdown continues.
func (p *Proxy) FlushAccounts() {
	for _, m := range snapshotManagers(&p.clineMu, p.clineManagers) {
		if err := m.Flush(); err != nil {
			p.log.Printf("cline: account file not flushed: %v", err)
		}
	}

	p.codexMu.Lock()
	codexManager := p.codexManager
	p.codexMu.Unlock()
	if codexManager != nil {
		if err := codexManager.Flush(); err != nil {
			p.log.Printf("codex: account file not flushed: %v", err)
		}
	}

	for _, m := range snapshotManagers(&p.opencodeMu, p.opencodeManagers) {
		if err := m.Flush(); err != nil {
			p.log.Printf("opencode: account file not flushed: %v", err)
		}
	}

	for _, m := range snapshotManagers(&p.workbuddyMu, p.workbuddyManagers) {
		if err := m.Flush(); err != nil {
			p.log.Printf("workbuddy: account file not flushed: %v", err)
		}
	}
}

// snapshotManagers copies a manager map under its lock so flush work runs
// outside it.
func snapshotManagers[T any](mu *sync.Mutex, managers map[string]T) []T {
	mu.Lock()
	defer mu.Unlock()

	out := make([]T, 0, len(managers))
	for _, m := range managers {
		out = append(out, m)
	}

	return out
}

// ApplySettings swaps the request-shaping options that hot reload can change.
// The allowlist is copied so a caller's slice cannot be mutated under a live
// request; the values are read per request, so a swap applies at once.
func (p *Proxy) ApplySettings(forceStream, forwardClientHeaders bool, allowClientHeaders []string) {
	p.forceStream.Store(forceStream)
	p.forwardClientHeaders.Store(forwardClientHeaders)
	p.storeAllowClientHeaders(allowClientHeaders)
}

// SetDedupeTools turns the global tool-dedupe switch on or off. A leg's own
// dedupe_tools still works when this is off; when this is on, every leg
// dedupes.
func (p *Proxy) SetDedupeTools(enabled bool) {
	p.dedupeTools.Store(enabled)
}

// SetClampToolSchemas turns the global tool-schema depth clamp on or off and
// sets the depth budget every leg is held to. A non-positive maxDepth falls
// back to defaultToolSchemaMaxDepth.
func (p *Proxy) SetClampToolSchemas(enabled bool, maxDepth int) {
	p.clampTools.Store(enabled)
	if maxDepth <= 0 {
		maxDepth = defaultToolSchemaMaxDepth
	}
	p.toolSchemaMaxDepth.Store(int64(maxDepth))
}

// schemaDepthBudget is the depth budget for one request, defaulting when the
// config left it unset.
func (p *Proxy) schemaDepthBudget() int {
	if depth := p.toolSchemaMaxDepth.Load(); depth > 0 {
		return int(depth)
	}

	return defaultToolSchemaMaxDepth
}

func (p *Proxy) storeAllowClientHeaders(allow []string) {
	clone := append([]string(nil), allow...)
	p.allowClientHeaders.Store(&clone)
}

// clientHeaderAllowlist returns the configured allowlist, or nil when none was
// stored — both mean "forward everything not denied".
func (p *Proxy) clientHeaderAllowlist() []string {
	if allow := p.allowClientHeaders.Load(); allow != nil {
		return *allow
	}

	return nil
}

// captureQuota records the Codex rate-limit headers carried on one upstream
// response. Every response repeats them, so the snapshot stays current without
// any extra request; the warn line fires once per upward crossing of the
// threshold and re-arms after the value drops back below it.
func (p *Proxy) captureQuota(providerName, key string, header http.Header) {
	snapshot, ok := quota.ParseHeaders(header)
	if !ok {
		return
	}

	p.quotaMu.Lock()
	byKey := p.quotas[providerName]
	if byKey == nil {
		byKey = make(map[string]quota.Snapshot)
		p.quotas[providerName] = byKey
	}
	previous, had := byKey[key]
	byKey[key] = snapshot
	p.quotaMu.Unlock()

	if snapshot.Primary == nil {
		return
	}
	wasAbove := had && previous.Primary != nil && previous.Primary.UsedPercent >= quota.WarnPercent
	if snapshot.Primary.UsedPercent >= quota.WarnPercent && !wasAbove {
		p.log.Printf("codex quota: provider=%s key=%s primary=%d%% window=%dm resets_at=%s", providerName, maskKey(key), snapshot.Primary.UsedPercent, snapshot.Primary.WindowMinutes, quotaResetLabel(snapshot.Primary.ResetAt))
	}
}

func quotaResetLabel(resetAt int64) string {
	if resetAt == 0 {
		return "unknown"
	}

	return time.Unix(resetAt, 0).Format(time.RFC3339)
}

// Quota returns the last Codex rate-limit snapshot observed for one provider key.
func (p *Proxy) Quota(providerName, key string) (quota.Snapshot, bool) {
	p.quotaMu.Lock()
	defer p.quotaMu.Unlock()

	snapshot, ok := p.quotas[providerName][key]

	return snapshot, ok
}

// pruneQuotas drops quota state for providers that are no longer configured.
func (p *Proxy) pruneQuotas(reg *provider.Registry) {
	live := make(map[string]bool)
	for _, pc := range reg.ProviderConfigs() {
		if !pc.Disabled {
			live[pc.Name] = true
		}
	}

	p.quotaMu.Lock()
	defer p.quotaMu.Unlock()

	for name := range p.quotas {
		if !live[name] {
			delete(p.quotas, name)
		}
	}
}

func (p *Proxy) Models() []string {
	return p.registry.Load().AllModels()
}

func copyClientHeaders(dst, src *http.Request, allow []string) {
	allowed := make(map[string]bool, len(allow))
	for _, h := range allow {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for key, values := range src.Header {
		lk := strings.ToLower(key)
		if hopByHopHeaders[lk] || clientCredentialHeaders[lk] {
			continue
		}
		if len(allow) > 0 && !allowed[lk] {
			continue
		}
		dst.Header[key] = append([]string(nil), values...)
	}
}
