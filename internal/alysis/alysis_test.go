package alysis

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

func testStore(t *testing.T, gatewayKeys ...string) *AccountStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alysis-accounts.json")
	store := &AccountStore{path: path}
	for _, key := range gatewayKeys {
		account := Account{AccountID: "acc_" + key, GatewayKey: key, CreatedAt: time.Now()}
		if err := store.Add(account); err != nil {
			t.Fatal(err)
		}
	}

	return store
}

func TestAccountStoreRoundTrip(t *testing.T) {
	store := testStore(t, "slk_one")
	if err := store.Add(Account{AccountID: "acc_keyless"}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GatewayKeys(); len(got) != 1 || got[0] != "slk_one" {
		t.Fatalf("gateway keys = %#v, want [slk_one]", got)
	}
}

func TestAccountStoreDedupesGatewayKey(t *testing.T) {
	store := testStore(t, "slk_one")

	replacement := Account{AccountID: "acc_replaced", Email: "user@example.test", GatewayKey: "slk_one"}
	if err := store.Add(replacement); err != nil {
		t.Fatal(err)
	}
	if got := store.GatewayKeys(); len(got) != 1 || got[0] != "slk_one" {
		t.Fatalf("gateway keys = %#v, want [slk_one]", got)
	}

	reloaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GatewayKeys(); len(got) != 1 {
		t.Fatalf("persisted gateway keys = %#v, want one entry", got)
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

func TestDefaultAccountsPathPrefersEnvironment(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(accountsFileEnv, custom)

	if got := DefaultAccountsPath(); got != custom {
		t.Fatalf("path = %q, want %q", got, custom)
	}

	os.Unsetenv(accountsFileEnv)
	if got := DefaultAccountsPath(); filepath.Base(got) != "alysis-accounts.json" {
		t.Fatalf("default path = %q, want alysis-accounts.json basename", got)
	}
}

func TestRequestDeviceAuthDecodesDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != AnonKey || r.Header.Get("Authorization") != "Bearer "+AnonKey {
			t.Errorf("anon headers = %q / %q", r.Header.Get("apikey"), r.Header.Get("Authorization"))
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["client_name"] != "routerllm" {
			t.Fatalf("client name payload = %#v", payload)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceAuth{DeviceCode: "device-1", UserCode: "ABCD-1234", Interval: 1, ExpiresIn: 900})
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), Endpoints: Endpoints{DeviceCode: server.URL + "/device-code"}}
	device, err := client.RequestDeviceAuth(context.Background(), "routerllm")
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceCode != "device-1" || device.UserCode != "ABCD-1234" {
		t.Fatalf("device = %#v", device)
	}
}

func TestRequestDeviceAuthNon200IncludesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too many pending device codes", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), Endpoints: Endpoints{DeviceCode: server.URL}}
	_, err := client.RequestDeviceAuth(context.Background(), "routerllm")
	if err == nil || !strings.Contains(err.Error(), "too many pending device codes") {
		t.Fatalf("err = %v, want truncated upstream body", err)
	}
}

func TestActivationURLPinsHost(t *testing.T) {
	client := &Client{}
	got := client.ActivationURL(DeviceAuth{
		UserCode:        "ABCD-1234",
		VerificationURI: "https://evil.example/other?x=1",
	})
	want := "https://alysiscode.com/other?x=1"
	if got != want {
		t.Fatalf("activation url = %q, want %q", got, want)
	}
}

func TestActivationURLFallsBackWhenServerURLMissing(t *testing.T) {
	client := &Client{}
	got := client.ActivationURL(DeviceAuth{UserCode: "ABCD-1234"})
	want := "https://alysiscode.com/activate?code=ABCD-1234"
	if got != want {
		t.Fatalf("activation url = %q, want %q", got, want)
	}
}

func TestPollForTokenReturnsKeyAfterApproval(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["device_code"] != "device-1" {
			t.Fatalf("device code payload = %#v", payload)
		}

		w.Header().Set("Content-Type", "application/json")
		if polls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"status": "approved", "key": "slk_gateway"})
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), minPollInterval: time.Millisecond, Endpoints: Endpoints{DeviceToken: server.URL + "/device-token"}}
	key, err := client.PollForToken(context.Background(), DeviceAuth{DeviceCode: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "slk_gateway" {
		t.Fatalf("key = %q, want slk_gateway", key)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
}

func TestPollForTokenDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "denied"})
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), minPollInterval: time.Millisecond, Endpoints: Endpoints{DeviceToken: server.URL}}
	_, err := client.PollForToken(context.Background(), DeviceAuth{DeviceCode: "device-1"})
	if err == nil || err.Error() != "alysis login was rejected on the website" {
		t.Fatalf("err = %v, want alysis login was rejected on the website", err)
	}
}

func TestPollForTokenExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "expired"})
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), minPollInterval: time.Millisecond, Endpoints: Endpoints{DeviceToken: server.URL}}
	_, err := client.PollForToken(context.Background(), DeviceAuth{DeviceCode: "device-1"})
	if err == nil || err.Error() != "alysis login code expired — run again" {
		t.Fatalf("err = %v, want alysis login code expired — run again", err)
	}
}

func TestVerifyKeyListsModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer slk_gateway" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "aly-fast"}, {"id": "aly-max"}}})
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), Endpoints: Endpoints{Models: server.URL}}
	models, err := client.VerifyKey(context.Background(), "slk_gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "aly-fast" || models[1] != "aly-max" {
		t.Fatalf("models = %#v", models)
	}
}
