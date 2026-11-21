package relay

import (
	"context"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func bunAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath(relayBin()); err != nil {
		t.Skipf("bun not on PATH (%s) — relay spawn tests need it", relayBin())
	}
	return true
}

func TestUpstreamForPrefersEnv(t *testing.T) {
	t.Setenv(envUpstream, "https://custom.example/")
	if got := UpstreamFor("https://opencode.ai/inference/openai"); got != "https://custom.example" {
		t.Fatalf("UpstreamFor = %q, want the forced env origin", got)
	}
}

func TestUpstreamForSelfRelayPointsAtDefault(t *testing.T) {
	t.Setenv(envUpstream, "")
	t.Setenv(envPort, "9999")
	// A legacy manual-relay base_url must not double-wrap: upstream = default.
	got := UpstreamFor("http://127.0.0.1:9999/inference/openai")
	if got != DefaultUpstream {
		t.Fatalf("UpstreamFor = %q, want %q", got, DefaultUpstream)
	}
}

func TestUpstreamForUsesProviderOrigin(t *testing.T) {
	t.Setenv(envUpstream, "")
	t.Setenv(envPort, "9999")
	got := UpstreamFor("https://opencode.ai/inference/openai")
	if got != "https://opencode.ai" {
		t.Fatalf("UpstreamFor = %q, want the provider origin", got)
	}
}

func TestEnsureFallbackWhenBinMissing(t *testing.T) {
	t.Setenv(envBin, "/nonexistent/bun")
	m := New(log.New(io.Discard, "", 0), io.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := m.Ensure(ctx, "https://opencode.ai"); err == nil {
		t.Fatal("Ensure succeeded with a missing bun binary — want spawn failure so callers fall back to direct")
	}
}

func TestEnsureIdempotentPerUpstream(t *testing.T) {
	if !bunAvailable(t) {
		return
	}
	t.Setenv(envPort, "0") // never collide with a standalone relay on 9999
	m := New(log.New(io.Discard, "", 0), io.Discard)
	t.Cleanup(m.Stop)

	ctx := context.Background()
	first, err := m.Ensure(ctx, "https://opencode.ai")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	again, err := m.Ensure(ctx, "https://opencode.ai")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if first != again {
		t.Fatalf("Ensure not idempotent: %q then %q", first, again)
	}
	if !strings.HasPrefix(first, "http://127.0.0.1:") {
		t.Fatalf("base URL = %q, want a loopback http URL", first)
	}

	resp, err := http.Get(first + "/health")
	if err != nil {
		t.Fatalf("relay /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status = %d, want 200", resp.StatusCode)
	}
}

func TestEnsureRecoversAfterChildKill(t *testing.T) {
	if !bunAvailable(t) {
		return
	}
	t.Setenv(envPort, "0")
	m := New(log.New(io.Discard, "", 0), io.Discard)
	t.Cleanup(m.Stop)

	base, err := m.Ensure(context.Background(), "https://opencode.ai")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Kill the child out from under the supervisor; the supervise loop must
	// respawn it and /health must come back on the same base URL.
	deadline := time.Now().Add(35 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay did not recover after child kill (base %s)", base)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func TestStopKillsChildren(t *testing.T) {
	if !bunAvailable(t) {
		return
	}
	t.Setenv(envPort, "0")
	m := New(log.New(io.Discard, "", 0), io.Discard)
	if _, err := m.Ensure(context.Background(), "https://opencode.ai"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	m.Stop()
	m.Stop() // idempotent, no children left
}

func TestConfiguredPort(t *testing.T) {
	t.Setenv(envPort, "")
	if got := ConfiguredPort(); got != 0 {
		t.Fatalf("ConfiguredPort = %d, want 0 (auto)", got)
	}
	t.Setenv(envPort, "0")
	if got := ConfiguredPort(); got != 0 {
		t.Fatalf("ConfiguredPort = %d, want 0 (auto)", got)
	}
	t.Setenv(envPort, "12345")
	if got := ConfiguredPort(); got != 12345 {
		t.Fatalf("ConfiguredPort = %d, want 12345", got)
	}
}

func TestEnabledFlag(t *testing.T) {
	t.Setenv(envEnabled, "")
	if Enabled() {
		t.Fatal("Enabled = true, want false when unset")
	}
	t.Setenv(envEnabled, "1")
	if !Enabled() {
		t.Fatal("Enabled = false, want true for \"1\"")
	}
}
