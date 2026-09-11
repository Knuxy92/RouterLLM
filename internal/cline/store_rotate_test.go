package cline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func refreshServer(t *testing.T, accessToken, refreshToken string, calls *int) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		}})
	}))
	t.Cleanup(server.Close)

	return server
}

// A concurrent `routerllm --cline-login` can add an account between load and
// rotation. Rotate must re-read the file and merge it, not persist a stale copy.
func TestRotateMergesAccountsAddedAfterLoad(t *testing.T) {
	store := testStore(t, "refresh-1")

	other, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Add(Account{AccountID: "acc_2", Email: "second@example.test", RefreshToken: "refresh-2"}); err != nil {
		t.Fatal(err)
	}

	if err := store.Rotate("refresh-1", "refresh-1b"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	tokens := reloaded.RefreshTokens()
	want := map[string]bool{"refresh-1b": true, "refresh-2": true}
	if len(tokens) != 2 || !want[tokens[0]] || !want[tokens[1]] {
		t.Fatalf("tokens = %#v, want [refresh-1b refresh-2]", tokens)
	}
}

// The account file can be unwritable (read-only Docker mount). A failed persist
// must not fail the request: the rotated token already lives in memory, and the
// caller would otherwise mark a healthy key dead.
func TestManagerServesTokenWhenRotationCannotPersist(t *testing.T) {
	calls := 0
	server := refreshServer(t, "access-1", "refresh-2", &calls)

	dir := t.TempDir()
	store := &AccountStore{
		path:     filepath.Join(dir, "cline-accounts.json"),
		accounts: []Account{{AccountID: "acc_1", Email: "one@example.test", RefreshToken: "refresh-1"}},
	}
	if err := store.persist(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Refresh: server.URL}}, store)

	token, err := manager.AccessToken(context.Background(), "refresh-1", false)
	if err != nil {
		t.Fatalf("AccessToken() error = %v, want nil when the store cannot persist", err)
	}
	if token != "workos:access-1" {
		t.Fatalf("token = %q, want workos:access-1", token)
	}

	if _, err := manager.AccessToken(context.Background(), "refresh-1", false); err != nil {
		t.Fatalf("cached AccessToken() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (rotated token served from memory)", calls)
	}
}
