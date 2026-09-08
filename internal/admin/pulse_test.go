package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"routerllm/internal/telemetry"
)

func getPulse(t *testing.T, srv http.Handler, session string) Pulse {
	t.Helper()

	w := request(t, srv, http.MethodGet, "/admin/api/pulse", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("pulse status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var p Pulse
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("pulse body: %v", err)
	}

	return p
}

func TestPulseRequiresSession(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	if w := request(t, srv, http.MethodGet, "/admin/api/pulse", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("pulse without session = %d, want 401", w.Code)
	}
}

func TestPulseStableWhenIdle(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	first := getPulse(t, srv, session)
	second := getPulse(t, srv, session)

	if first.StatusSig != second.StatusSig {
		t.Fatalf("status sig moved while idle: %q vs %q", first.StatusSig, second.StatusSig)
	}
	if first.MetricsSig != second.MetricsSig {
		t.Fatalf("metrics sig moved while idle: %q vs %q", first.MetricsSig, second.MetricsSig)
	}
}

func TestPulseTracksConfigAndKeyChanges(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	before := getPulse(t, srv, session)

	w := request(t, srv, http.MethodPost, "/admin/api/providers/alpha/keys/0", session, `{"disabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("key toggle status = %d, want 200: %s", w.Code, w.Body.String())
	}
	after := getPulse(t, srv, session)

	if before.StatusSig == after.StatusSig {
		t.Fatal("status sig unchanged after manual key disable")
	}
	if before.MetricsSig != after.MetricsSig {
		t.Fatal("metrics sig should not move on a key toggle")
	}
}

func TestPulseTracksTelemetrySeq(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))

	store, err := telemetry.NewStore(filepath.Join(t.TempDir(), "t.jsonl"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	deps.Telemetry = store

	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	before := getPulse(t, srv, session)
	if before.Seq != 0 {
		t.Fatalf("seq = %d, want 0 before any events", before.Seq)
	}

	store.Record(telemetry.Event{Model: "model-a", Provider: "alpha", Status: 200})
	after := getPulse(t, srv, session)

	if after.Seq != 1 {
		t.Fatalf("seq = %d, want 1 after one event", after.Seq)
	}
	if before.MetricsSig == after.MetricsSig {
		t.Fatal("metrics sig unchanged after a new event")
	}
	if before.StatusSig == after.StatusSig {
		t.Fatal("status sig unchanged after a new event (leg notes derive from telemetry)")
	}
}

func TestAdminAPIRespondsGzipped(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	req, _ := http.NewRequest(http.MethodGet, "/admin/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
}
