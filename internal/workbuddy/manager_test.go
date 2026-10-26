package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAccessTokenSingleflight shares one upstream refresh across concurrent
// callers holding the same refresh token.
func TestAccessTokenSingleflight(t *testing.T) {
	var refreshes atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc(RefreshPath, func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"accessToken":  "access-1",
			"refreshToken": "refresh-1-rotated",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		}})
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	t.Setenv("WORKBUDDY_INTL_CHAT_BASE", upstream.URL)

	store, err := LoadAccountStore(t.TempDir() + "/wb-accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Account{UID: "uid-1", RefreshToken: "refresh-1", Realm: "intl", Product: "desktop", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(&Client{HTTPClient: upstream.Client()}, store)

	const callers = 5
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = manager.AccessToken(context.Background(), "intl", "uid-1", "refresh-1", false)
		}(i)
	}
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if tokens[i] != "access-1" {
			t.Fatalf("caller %d token = %q", i, tokens[i])
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("upstream refreshes = %d, want 1", got)
	}

	// The rotation persisted into the account store.
	stored, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := stored.RefreshTokens()
	if len(got) != 1 || got[0] != "refresh-1-rotated" {
		t.Fatalf("stored refresh tokens = %v, want [refresh-1-rotated]", got)
	}
}
