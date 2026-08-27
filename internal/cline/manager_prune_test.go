package cline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Refresh tokens rotate and each config reload re-reads the account file, so the
// key set churns. Without pruning, tokens map keeps one entry per historical
// token for the process lifetime.
func TestManagerDropsExpiredTokensOnRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access-live",
			"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
		}})
	}))
	defer server.Close()

	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Refresh: server.URL}}, nil)

	manager.mu.Lock()
	manager.tokens["stale-rotated"] = Token{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour)}
	manager.tokens["empty-access"] = Token{ExpiresAt: time.Now().Add(time.Hour)}
	manager.tokens["still-valid"] = Token{AccessToken: "valid", ExpiresAt: time.Now().Add(time.Hour)}
	manager.mu.Unlock()

	if _, err := manager.AccessToken(context.Background(), "refresh-new", false); err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	if _, ok := manager.tokens["stale-rotated"]; ok {
		t.Error("expired token was retained")
	}
	if _, ok := manager.tokens["empty-access"]; ok {
		t.Error("token with no access token was retained")
	}
	if _, ok := manager.tokens["still-valid"]; !ok {
		t.Error("unexpired token was pruned")
	}
	if _, ok := manager.tokens["refresh-new"]; !ok {
		t.Error("freshly refreshed token was not cached")
	}
	if len(manager.tokens) != 2 {
		t.Fatalf("tokens len = %d, want 2 (still-valid + refresh-new)", len(manager.tokens))
	}
}

func TestManagerTokenCacheDoesNotGrowAcrossRotations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken": "access",
			"expiresAt":   time.Now().Add(-time.Minute).UnixMilli(),
		}})
	}))
	defer server.Close()

	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Refresh: server.URL}}, nil)

	for i := 0; i < 50; i++ {
		if _, err := manager.AccessToken(context.Background(), "rotated-token", false); err != nil {
			t.Fatal(err)
		}
	}

	manager.mu.Lock()
	size := len(manager.tokens)
	manager.mu.Unlock()

	if size > 1 {
		t.Fatalf("tokens len = %d after 50 rotations, want at most 1", size)
	}
}
