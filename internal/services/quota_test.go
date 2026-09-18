package services

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"routerllm/internal/codex"
	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func TestParseQuotaHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex-Primary-Used-Percent", "42")
	header.Set("X-Codex-Primary-Window-Minutes", "43200")
	header.Set("X-Codex-Primary-Reset-At", "1792294116")
	header.Set("X-Codex-Secondary-Used-Percent", "0")
	header.Set("X-Codex-Secondary-Window-Minutes", "0")
	header.Set("X-Codex-Plan-Type", "free")

	snapshot, ok := parseQuotaHeaders(header)
	if !ok {
		t.Fatal("expected a snapshot")
	}
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 42 || snapshot.Primary.WindowMinutes != 43200 || snapshot.Primary.ResetAt != 1792294116 {
		t.Fatalf("primary = %+v", snapshot.Primary)
	}
	if snapshot.Secondary != nil {
		t.Fatalf("zero-length secondary window must be dropped: %+v", snapshot.Secondary)
	}
	if snapshot.PlanType != "free" {
		t.Fatalf("plan = %q, want free", snapshot.PlanType)
	}
}

func TestParseQuotaHeadersWithoutCodexHeaders(t *testing.T) {
	if _, ok := parseQuotaHeaders(http.Header{}); ok {
		t.Fatal("plain headers must not produce a snapshot")
	}
	if _, ok := parseQuotaHeaders(nil); ok {
		t.Fatal("nil headers must not produce a snapshot")
	}
}

// quotaProxy builds a codex proxy whose fake upstream answers /oauth/token and
// /codex/responses, with the quota headers driven by percent.
func quotaProxy(t *testing.T, percent *int, logger *log.Logger) *Proxy {
	t.Helper()
	t.Setenv("CODEX_ACCOUNTS_FILE", filepath.Join(t.TempDir(), "codex-accounts.json"))

	refreshes := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", codexTokenHandler(t, &refreshes))
	mux.HandleFunc("/codex/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Codex-Primary-Used-Percent", strconv.Itoa(*percent))
		w.Header().Set("X-Codex-Primary-Window-Minutes", "43200")
		w.Header().Set("X-Codex-Primary-Reset-At", "1792294116")
		w.Header().Set("X-Codex-Plan-Type", "free")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexChatSSE)
	})
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)

	proxy := NewProxy(codexRegistry(upstream.URL, "refresh-1"), upstream.Client(), logger, false, false, false, false, nil, "")
	proxy.codexMu.Lock()
	proxy.codexManager = codex.NewManager(&codex.Client{
		HTTPClient: upstream.Client(),
		Endpoints:  codex.Endpoints{Token: upstream.URL + "/oauth/token"},
	}, nil)
	proxy.codexMu.Unlock()

	return proxy
}

func quotaHit(t *testing.T, proxy *Proxy) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"codex-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardCapturesCodexQuota(t *testing.T) {
	percent := 42
	proxy := quotaProxy(t, &percent, log.New(io.Discard, "", 0))

	quotaHit(t, proxy)

	snapshot, ok := proxy.Quota("codex", "refresh-1")
	if !ok {
		t.Fatal("expected a quota snapshot for the used key")
	}
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 42 || snapshot.Primary.ResetAt != 1792294116 {
		t.Fatalf("primary = %+v", snapshot.Primary)
	}
	if snapshot.PlanType != "free" {
		t.Fatalf("plan = %q, want free", snapshot.PlanType)
	}

	percent = 55
	quotaHit(t, proxy)
	snapshot, _ = proxy.Quota("codex", "refresh-1")
	if snapshot.Primary.UsedPercent != 55 {
		t.Fatalf("used percent = %d, want the latest 55", snapshot.Primary.UsedPercent)
	}

	if _, ok := proxy.Quota("codex", "other"); ok {
		t.Fatal("unknown key must not resolve a snapshot")
	}
}

func TestForwardWarnsOncePerQuotaCrossing(t *testing.T) {
	percent := 10
	var buf bytes.Buffer
	proxy := quotaProxy(t, &percent, log.New(&buf, "", 0))

	quotaHit(t, proxy)
	if strings.Contains(buf.String(), "codex quota") {
		t.Fatalf("no warning expected below the threshold: %s", buf.String())
	}

	percent = 85
	quotaHit(t, proxy)
	if got := strings.Count(buf.String(), "codex quota"); got != 1 {
		t.Fatalf("warnings = %d, want 1 after the first crossing", got)
	}

	percent = 90
	quotaHit(t, proxy)
	if got := strings.Count(buf.String(), "codex quota"); got != 1 {
		t.Fatalf("warnings = %d, want still 1 while above the threshold", got)
	}

	percent = 10
	quotaHit(t, proxy)
	percent = 85
	quotaHit(t, proxy)
	if got := strings.Count(buf.String(), "codex quota"); got != 2 {
		t.Fatalf("warnings = %d, want a second warning after a re-arming dip", got)
	}

	if !strings.Contains(buf.String(), "key=...sh-1") {
		t.Fatalf("warning must mask the key: %s", buf.String())
	}
}

func TestApplyPrunesQuotasForRemovedProviders(t *testing.T) {
	proxy := NewProxy(provider.NewRegistry(nil, nil, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.quotas["gone"] = map[string]QuotaSnapshot{"k": {}}
	proxy.quotas["kept"] = map[string]QuotaSnapshot{"k": {}}

	next := provider.NewRegistry([]config.ProviderConfig{{
		Name: "kept", BaseURL: "https://example.test", Style: "openai", Keys: []string{"k"},
	}}, []model.Rule{{
		ModelID: "m", Routes: []model.Spec{{Provider: "kept", Model: "m"}},
	}}, time.Minute)
	proxy.Apply(next, "")

	if _, ok := proxy.Quota("gone", "k"); ok {
		t.Fatal("quota state for a removed provider must be pruned")
	}
	if _, ok := proxy.Quota("kept", "k"); !ok {
		t.Fatal("quota state for a live provider must survive")
	}
}
