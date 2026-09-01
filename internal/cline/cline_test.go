package cline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T, refreshTokens ...string) *AccountStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cline-accounts.json")
	store := &AccountStore{path: path}
	for _, token := range refreshTokens {
		if err := store.Add(Account{AccountID: "acc_" + token, Email: token + "@example.test", RefreshToken: token}); err != nil {
			t.Fatal(err)
		}
	}

	return store
}

func TestAccountStoreRoundTrip(t *testing.T) {
	store := testStore(t, "refresh-1")

	reloaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.RefreshTokens(); len(got) != 1 || got[0] != "refresh-1" {
		t.Fatalf("refresh tokens = %#v, want [refresh-1]", got)
	}

	if err := store.Rotate("refresh-1", "refresh-2"); err != nil {
		t.Fatal(err)
	}
	rotated, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := rotated.RefreshTokens(); len(got) != 1 || got[0] != "refresh-2" {
		t.Fatalf("rotated tokens = %#v, want [refresh-2]", got)
	}
}

func TestLoadAccountStoreMissingFileIsEmpty(t *testing.T) {
	store, err := LoadAccountStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.RefreshTokens()) != 0 {
		t.Fatal("expected no refresh tokens")
	}
}

func TestDefaultAccountsPathPrefersEnvironment(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(accountsFileEnv, custom)

	if got := DefaultAccountsPath(); got != custom {
		t.Fatalf("path = %q, want %q", got, custom)
	}

	os.Unsetenv(accountsFileEnv)
	if got := DefaultAccountsPath(); filepath.Base(got) != "cline-accounts.json" {
		t.Fatalf("default path = %q, want cline-accounts.json basename", got)
	}
}

func TestManagerRefreshesAndCachesAccessToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["refreshToken"] != "refresh-1" || payload["grantType"] != "refresh_token" {
			t.Fatalf("refresh payload = %#v", payload)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken":  "access-1",
			"refreshToken": "refresh-2",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		}})
	}))
	defer server.Close()

	store := testStore(t, "refresh-1")
	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Refresh: server.URL}}, store)

	token, err := manager.AccessToken(context.Background(), "refresh-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if token != "workos:access-1" {
		t.Fatalf("token = %q, want workos:access-1", token)
	}

	if _, err := manager.AccessToken(context.Background(), "refresh-1", false); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (cached)", calls)
	}

	if _, err := manager.AccessToken(context.Background(), "refresh-1", true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("refresh calls = %d, want 2 after forced refresh", calls)
	}

	persisted, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.RefreshTokens(); len(got) != 1 || got[0] != "refresh-2" {
		t.Fatalf("persisted tokens = %#v, want rotated [refresh-2]", got)
	}
}

func TestManagerLoginStoresRefreshTokenOnly(t *testing.T) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("client_id"); got != WorkOSClientID {
			t.Fatalf("client_id = %q", got)
		}
		_ = json.NewEncoder(w).Encode(DeviceAuth{
			DeviceCode:              "device-1",
			UserCode:                "ABCD-EFGH",
			VerificationURIComplete: "https://example.test/activate?code=ABCD-EFGH",
			Interval:                1,
			ExpiresIn:               60,
		})
	})
	mux.HandleFunc("/authenticate", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "workos-access", "refresh_token": "workos-refresh"})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["accessToken"] != "workos-access" || payload["refreshToken"] != "workos-refresh" {
			t.Fatalf("register payload = %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"accessToken":  "cline-access",
			"refreshToken": "cline-refresh",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
			"userInfo":     map[string]string{"email": "user@example.test"},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	store := testStore(t)
	manager := NewManager(&Client{HTTPClient: server.Client(), MinPollInterval: 10 * time.Millisecond, Endpoints: Endpoints{
		DeviceAuth:   server.URL + "/device",
		Authenticate: server.URL + "/authenticate",
		Register:     server.URL + "/register",
	}}, store)

	notified := false
	account, err := manager.Login(context.Background(), func(device DeviceAuth) {
		notified = true
		if device.UserCode != "ABCD-EFGH" {
			t.Fatalf("user code = %q", device.UserCode)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !notified {
		t.Fatal("expected login notification")
	}
	if account.Email != "user@example.test" || account.RefreshToken != "cline-refresh" {
		t.Fatalf("account = %#v", account)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}

	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("expected persisted accounts")
	}
	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Accounts) != 1 || file.Accounts[0].RefreshToken != "cline-refresh" {
		t.Fatalf("persisted accounts = %#v", file.Accounts)
	}
	if strings.Contains(string(raw), "cline-access") {
		t.Fatal("access token must not be written to disk")
	}
}

func TestPrepareBodyAndHeaders(t *testing.T) {
	body := map[string]any{}
	sessionID := PrepareBody(body)

	if sessionID == "" || body["session_id"] != sessionID {
		t.Fatalf("session id = %q, body = %#v", sessionID, body)
	}
	if body["model"] != defaultModel || body["max_tokens"] != defaultMaxTokens || body["reasoning_effort"] != defaultEffort {
		t.Fatalf("body defaults = %#v", body)
	}

	header := http.Header{}
	SetHeaders(header, "access-1", sessionID)
	for key, want := range map[string]string{
		"Authorization":    "Bearer workos:access-1",
		"X-Task-ID":        sessionID,
		"X-CLIENT-TYPE":    "cline-sdk",
		"X-CLIENT-VERSION": clientVersion,
		"X-PLATFORM":       "terminal",
		"User-Agent":       clientUserAgent,
		"HTTP-Referer":     "https://cline.bot",
		"X-Title":          "Cline",
	} {
		if got := header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestPrepareBodyKeepsClientValues(t *testing.T) {
	body := map[string]any{"model": "cline-free/other", "max_tokens": 10, "reasoning_effort": "low", "session_id": "sess_fixed"}
	if got := PrepareBody(body); got != "sess_fixed" {
		t.Fatalf("session id = %q", got)
	}
	if body["model"] != "cline-free/other" || body["max_tokens"] != 10 || body["reasoning_effort"] != "low" {
		t.Fatalf("body = %#v", body)
	}
}
