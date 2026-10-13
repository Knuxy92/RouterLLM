package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerCachesAccessToken(t *testing.T) {
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc-1","refresh_token":"ref-2","expires_in":3600}`))
	}))
	defer server.Close()

	manager := NewManager(testClient(server), nil)

	first, err := manager.AccessToken(context.Background(), "ref-1", false)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	second, err := manager.AccessToken(context.Background(), "ref-1", false)
	if err != nil {
		t.Fatalf("AccessToken cached: %v", err)
	}
	if first != "acc-1" || second != "acc-1" {
		t.Fatalf("tokens = %q, %q, want acc-1", first, second)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 (cache hit)", refreshes)
	}

	forced, err := manager.AccessToken(context.Background(), "ref-1", true)
	if err != nil {
		t.Fatalf("AccessToken forced: %v", err)
	}
	if forced != "acc-1" || refreshes != 2 {
		t.Fatalf("forced = %q refreshes = %d, want acc-1/2", forced, refreshes)
	}
}

func TestManagerPersistsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode-accounts.json")
	store, err := LoadAccountStore(path)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}
	if err := store.Add(Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc-1","refresh_token":"ref-2","expires_in":3600}`))
	}))
	defer server.Close()

	manager := NewManager(testClient(server), store)
	if _, err := manager.AccessToken(context.Background(), "ref-1", false); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	tokens := store.RefreshTokens()
	if len(tokens) != 1 || tokens[0] != "ref-2" {
		t.Fatalf("RefreshTokens = %v, want [ref-2]", tokens)
	}
}

func TestManagerLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode-accounts.json")
	store, err := LoadAccountStore(path)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}

	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/device/code":
			_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"USER-1","verification_uri_complete":"/console/device?user_code=USER-1","expires_in":60,"interval":0}`))
		case "/auth/device/token":
			polls++
			if polls < 2 {
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"acc-1","refresh_token":"ref-1","expires_in":3600,"org_id":"org-1"}`))
		case "/api/user":
			_, _ = w.Write([]byte(`{"id":"u-1","email":"dev@example.com"}`))
		case "/api/orgs":
			_, _ = w.Write([]byte(`[{"id":"org-1","name":"Acme"}]`))
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	manager := NewManager(testClient(server), store)
	var notified DeviceAuth
	account, err := manager.Login(context.Background(), func(device DeviceAuth) { notified = device })
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if notified.UserCode != "USER-1" || notified.CompleteURI == "" {
		t.Fatalf("notified = %+v", notified)
	}
	if account.Email != "dev@example.com" || account.RefreshToken != "ref-1" {
		t.Fatalf("account = %+v", account)
	}
	if account.Server != server.URL {
		t.Fatalf("Server = %q, want test server", account.Server)
	}
	if account.OrgID != "org-1" || account.OrgName != "Acme" {
		t.Fatalf("org = %q/%q", account.OrgID, account.OrgName)
	}

	// Login primes the cache, so the next request needs no refresh.
	cached, err := manager.AccessToken(context.Background(), "ref-1", false)
	if err != nil {
		t.Fatalf("AccessToken after login: %v", err)
	}
	if cached != "acc-1" {
		t.Fatalf("cached = %q, want acc-1", cached)
	}
}

func TestManagerLoginEmptyRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/device/code":
			_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"USER-1","verification_uri_complete":"/d","expires_in":60,"interval":0}`))
		case "/auth/device/token":
			_, _ = w.Write([]byte(`{"access_token":"acc-1","expires_in":3600}`))
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "opencode-accounts.json")
	store, err := LoadAccountStore(path)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}

	manager := NewManager(testClient(server), store)
	if _, err := manager.Login(context.Background(), nil); err == nil {
		t.Fatal("empty refresh token: want error")
	}
}
