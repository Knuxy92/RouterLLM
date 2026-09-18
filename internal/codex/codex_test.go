package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	path := filepath.Join(t.TempDir(), "codex-accounts.json")
	store := &AccountStore{path: path}
	for _, token := range refreshTokens {
		if err := store.Add(Account{AccountID: "acc_" + token, Email: token + "@example.test", RefreshToken: token}); err != nil {
			t.Fatal(err)
		}
	}

	return store
}

func testJWT(t *testing.T, email, accountID string, expiresAt time.Time) string {
	t.Helper()

	claims := map[string]any{"exp": expiresAt.Unix()}
	if email != "" {
		claims[profileClaimKey] = map[string]any{"email": email}
	}
	if accountID != "" {
		claims[authClaimKey] = map[string]any{accountIDClaim: accountID}
	}

	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}

	return "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}

func refreshServer(t *testing.T, accessToken, refreshToken string, calls *int) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++

		if got := r.PostFormValue("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.PostFormValue("client_id"); got != codexClientID {
			t.Errorf("client_id = %q, want %q", got, codexClientID)
		}
		if got := r.PostFormValue("scope"); got != oauthScope {
			t.Errorf("scope = %q, want %q", got, oauthScope)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"expires_in":    3600,
		})
	}))
	t.Cleanup(server.Close)

	return server
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
	if got := DefaultAccountsPath(); filepath.Base(got) != "codex-accounts.json" {
		t.Fatalf("default path = %q, want codex-accounts.json basename", got)
	}
}

func TestManagerRefreshesAndCachesAccessToken(t *testing.T) {
	calls := 0
	accessToken := testJWT(t, "user@example.test", "acct-1", time.Now().Add(time.Hour))
	server := refreshServer(t, accessToken, "refresh-2", &calls)

	store := testStore(t, "refresh-1")
	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Token: server.URL}}, store)

	token, err := manager.AccessToken(context.Background(), "refresh-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if token != accessToken {
		t.Fatalf("token = %q, want %q", token, accessToken)
	}

	if _, err := manager.AccessToken(context.Background(), "refresh-1", false); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (cached)", calls)
	}

	persisted, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.RefreshTokens(); len(got) != 1 || got[0] != "refresh-2" {
		t.Fatalf("persisted tokens = %#v, want rotated [refresh-2]", got)
	}
}

func TestManagerForcedRefreshAlwaysRefreshes(t *testing.T) {
	calls := 0
	accessToken := testJWT(t, "user@example.test", "acct-1", time.Now().Add(time.Hour))
	server := refreshServer(t, accessToken, "refresh-2", &calls)

	store := testStore(t, "refresh-1")
	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Token: server.URL}}, store)

	for i := 0; i < 2; i++ {
		if _, err := manager.AccessToken(context.Background(), "refresh-1", true); err != nil {
			t.Fatal(err)
		}
	}

	if calls != 2 {
		t.Fatalf("refresh calls = %d, want 2 with force=true", calls)
	}
}

func TestManagerLoginStoresRefreshTokenOnly(t *testing.T) {
	polls := 0
	exchanges := 0
	accessToken := testJWT(t, "user@example.test", "acct-1", time.Now().Add(time.Hour))

	mux := http.NewServeMux()
	mux.HandleFunc("/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode usercode body: %v", err)
		}
		if body["client_id"] != codexClientID {
			t.Errorf("client_id = %q, want %q", body["client_id"], codexClientID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_auth_id": "device-1",
			"user_code":      "ABCD-EFGH",
			"interval":       "1",
			"expires_at":     time.Now().Add(2 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		polls++

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode device token body: %v", err)
		}
		if body["device_auth_id"] != "device-1" || body["user_code"] != "ABCD-EFGH" {
			t.Errorf("device token body = %#v", body)
		}

		if polls == 1 {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"message": "Device authorization is pending. Please try again.",
				"type":    "invalid_request_error",
				"code":    "deviceauth_authorization_pending",
			}})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_code": "auth-code-1",
			"code_verifier":      "verifier-1",
		})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		exchanges++
		if got := r.PostFormValue("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", got)
		}
		if got := r.PostFormValue("code"); got != "auth-code-1" {
			t.Errorf("code = %q, want auth-code-1", got)
		}
		if got := r.PostFormValue("code_verifier"); got != "verifier-1" {
			t.Errorf("code_verifier = %q, want verifier-1", got)
		}
		if got := r.PostFormValue("redirect_uri"); got != deviceCallbackURI {
			t.Errorf("redirect_uri = %q, want %q", got, deviceCallbackURI)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": "codex-refresh",
			"expires_in":    3600,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	store := testStore(t)
	manager := NewManager(&Client{
		HTTPClient:      server.Client(),
		MinPollInterval: 10 * time.Millisecond,
		Endpoints: Endpoints{
			DeviceCode:  server.URL + "/deviceauth/usercode",
			DeviceToken: server.URL + "/deviceauth/token",
			Token:       server.URL + "/oauth/token",
		},
	}, store)

	notified := false
	account, err := manager.Login(context.Background(), func(device DeviceAuth) {
		notified = true
		if device.UserCode != "ABCD-EFGH" {
			t.Errorf("user code = %q, want ABCD-EFGH", device.UserCode)
		}
		if device.VerificationURI != deviceLoginURL {
			t.Errorf("verification uri = %q, want %q", device.VerificationURI, deviceLoginURL)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !notified {
		t.Fatal("expected login notification")
	}
	if account.Email != "user@example.test" {
		t.Fatalf("email = %q, want user@example.test", account.Email)
	}
	if !strings.HasPrefix(account.AccountID, "acc_") {
		t.Fatalf("account id = %q, want acc_ prefix", account.AccountID)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
	if exchanges != 1 {
		t.Fatalf("exchanges = %d, want 1", exchanges)
	}

	token, err := manager.AccessToken(context.Background(), "codex-refresh", false)
	if err != nil {
		t.Fatal(err)
	}
	if token != accessToken {
		t.Fatalf("token = %q, want cached %q", token, accessToken)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2 (login token served from cache)", polls)
	}

	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Accounts) != 1 || file.Accounts[0].RefreshToken != "codex-refresh" || file.Accounts[0].Email != "user@example.test" {
		t.Fatalf("persisted accounts = %#v", file.Accounts)
	}
	if strings.Contains(string(raw), accessToken) {
		t.Fatal("access token must not be written to disk")
	}
}

func TestRotateRewritesEntryHoldingOldRefreshToken(t *testing.T) {
	store := testStore(t, "refresh-1", "refresh-2")

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

// TestManagerRefreshNeverReusesSpentToken pins the rotation contract: the auth
// server spends a refresh token once, so every later refresh must send the
// newest token the previous rotation returned.
func TestManagerRefreshNeverReusesSpentToken(t *testing.T) {
	issued := 0
	valid := "refresh-1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.PostFormValue("refresh_token"); got != valid {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "refresh_token_reused"})
			return
		}

		issued++
		valid = fmt.Sprintf("rotated-%d", issued)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  testJWT(t, "user@example.test", "acct-1", time.Now().Add(time.Hour)),
			"refresh_token": valid,
			"expires_in":    3600,
		})
	}))
	t.Cleanup(server.Close)

	store := testStore(t, "refresh-1")
	manager := NewManager(&Client{HTTPClient: server.Client(), Endpoints: Endpoints{Token: server.URL}}, store)

	for i := 0; i < 3; i++ {
		if _, err := manager.AccessToken(context.Background(), "refresh-1", true); err != nil {
			t.Fatalf("refresh %d failed: %v", i+1, err)
		}
	}
	if issued != 3 {
		t.Fatalf("issued = %d, want 3", issued)
	}

	persisted, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.RefreshTokens(); len(got) != 1 || got[0] != "rotated-3" {
		t.Fatalf("persisted tokens = %#v, want [rotated-3]", got)
	}
}

func TestSetHeadersUsesAccountIDFromToken(t *testing.T) {
	accessToken := testJWT(t, "user@example.test", "acct-1", time.Now().Add(time.Hour))

	header := http.Header{}
	SetHeaders(header, accessToken)
	for key, want := range map[string]string{
		"Authorization":      "Bearer " + accessToken,
		"ChatGPT-Account-Id": "acct-1",
		"originator":         codexOriginator,
		"User-Agent":         codexUserAgent,
		"Content-Type":       "application/json",
		"OpenAI-Beta":        codexBetaHeader,
		"Accept":             "text/event-stream",
	} {
		if got := header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	header = http.Header{}
	SetHeaders(header, "not-a-jwt")
	if got := header.Get("ChatGPT-Account-Id"); got != "" {
		t.Fatalf("ChatGPT-Account-Id = %q, want empty without an account claim", got)
	}
	if got := header.Get("Authorization"); got != "Bearer not-a-jwt" {
		t.Fatalf("Authorization = %q, want Bearer not-a-jwt", got)
	}
}
