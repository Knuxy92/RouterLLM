package kilo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T, accounts ...Account) *AccountStore {
	t.Helper()

	store := &AccountStore{path: filepath.Join(t.TempDir(), "kilo-accounts.json")}
	for _, account := range accounts {
		if err := store.Add(account); err != nil {
			t.Fatal(err)
		}
	}

	return store
}

func TestAccountStoreRoundTrip(t *testing.T) {
	store := testStore(t,
		Account{AccountID: "acc_1", Email: "one@example.test", Token: "tok_one", OrganizationID: "org-1", CreatedAt: time.Now()},
		Account{AccountID: "acc_2", Token: "tok_two", CreatedAt: time.Now()},
	)
	if err := store.Add(Account{AccountID: "acc_keyless"}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GatewayKeys(); !reflect.DeepEqual(got, []string{"tok_one", "tok_two"}) {
		t.Fatalf("gateway keys = %#v", got)
	}
	if got := reloaded.OrganizationID("tok_one"); got != "org-1" {
		t.Fatalf("org for tok_one = %q, want org-1", got)
	}
	if got := reloaded.OrganizationID("tok_two"); got != "" {
		t.Fatalf("org for tok_two = %q, want empty", got)
	}
}

func TestAccountStoreDedupesToken(t *testing.T) {
	store := testStore(t, Account{AccountID: "acc_1", Token: "tok_one"})
	if err := store.Add(Account{AccountID: "acc_replaced", Email: "user@example.test", Token: "tok_one"}); err != nil {
		t.Fatal(err)
	}
	if got := store.GatewayKeys(); !reflect.DeepEqual(got, []string{"tok_one"}) {
		t.Fatalf("gateway keys = %#v", got)
	}
}

func TestLoadAccountStoreMissingFileIsEmpty(t *testing.T) {
	store, err := LoadAccountStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.GatewayKeys()) != 0 {
		t.Fatal("expected no gateway keys")
	}
}

func TestAccountStorePersistLeavesNoTempFilesBehind(t *testing.T) {
	store := testStore(t, Account{AccountID: "acc_1", Token: "tok_one"})

	dir := filepath.Dir(store.Path())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(store.Path()) {
		t.Fatalf("directory entries = %v, want only the accounts file", entries)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("accounts file mode = %o, want 600", perm)
		}
	}
}

func TestDefaultAccountsPathPrefersEnvironment(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(accountsFileEnv, custom)

	if got := DefaultAccountsPath(); got != custom {
		t.Fatalf("path = %q, want %q", got, custom)
	}

	os.Unsetenv(accountsFileEnv)
	if got := DefaultAccountsPath(); filepath.Base(got) != "kilo-accounts.json" {
		t.Fatalf("default path = %q, want kilo-accounts.json basename", got)
	}
}

func TestRequestDeviceAuthDecodesDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("method = %s, content-type = %q", r.Method, r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{"code":"SHORT","user_code":"ABCD-BAE5","device_code":"LONG","verificationUrl":"https://app.kilo.ai/device-auth?code=ABCD-BAE5","expiresIn":599}`))
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL}
	device, err := client.RequestDeviceAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if device.Code != "SHORT" || device.UserCode != "ABCD-BAE5" || device.ExpiresIn != 599 {
		t.Fatalf("device = %#v", device)
	}
}

func TestRequestDeviceAuthNon200IncludesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too many pending device codes", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL}
	_, err := client.RequestDeviceAuth(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too many pending device codes") {
		t.Fatalf("err = %v, want truncated upstream body", err)
	}
}

func TestPollForTokenUsesShortCode(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if !strings.HasSuffix(r.URL.Path, "/api/device-auth/codes/SHORT") {
			t.Fatalf("poll path = %q, want short code", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if polls == 1 {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"approved","token":"tok_live","userEmail":"user@example.test"}`))
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, pollInterval: time.Millisecond}
	token, err := client.PollForToken(context.Background(), DeviceAuth{Code: "SHORT", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok_live" || polls != 2 {
		t.Fatalf("token = %q, polls = %d", token, polls)
	}
}

func TestPollForTokenDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, pollInterval: time.Millisecond}
	_, err := client.PollForToken(context.Background(), DeviceAuth{Code: "SHORT", ExpiresIn: 60})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want denial", err)
	}
}

func TestPollForTokenExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, pollInterval: time.Millisecond}
	_, err := client.PollForToken(context.Background(), DeviceAuth{Code: "SHORT", ExpiresIn: 60})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want expiry", err)
	}
}

func TestFetchProfileAndGatewayModelsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok_live" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"user":{"email":"user@example.test"},"selectedOrganizationId":"org-1","hasPersonalAccount":true}`))
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, GatewayBase: "https://gateway.example/api/v1"}
	profile, err := client.FetchProfile(context.Background(), "tok_live")
	if err != nil {
		t.Fatal(err)
	}
	if profile.SelectedOrganizationID != "org-1" || profile.User.Email != "user@example.test" {
		t.Fatalf("profile = %#v", profile)
	}
	if got := client.GatewayModelsURL("org-1"); got != "https://gateway.example/api/v1/organizations/org-1/models" {
		t.Fatalf("models url = %q", got)
	}
	if got := client.GatewayModelsURL(""); got != "https://gateway.example/api/v1/models" {
		t.Fatalf("personal models url = %q", got)
	}
}

func TestParseModels(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{name: "chat models with duplicates", body: `{"data":[{"id":"kilo-fast"},{"id":"kilo-fast"},{"id":"kilo-max"}]}`, want: []string{"kilo-fast", "kilo-max"}},
		{name: "empty", body: `{"data":[]}`, want: []string{}},
		{name: "blank ids", body: `{"data":[{"id":""},{"id":"kilo-fast"}]}`, want: []string{"kilo-fast"}},
		{name: "malformed", body: `{"data":[{"id":"kilo-fast"`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModels([]byte(tt.body))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "parse kilo models catalog") {
					t.Fatalf("err = %v, want wrapped parse error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("models = %#v, want %#v", got, tt.want)
			}
		})
	}
}
