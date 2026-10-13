package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(server *httptest.Server) *Client {
	return &Client{HTTPClient: server.Client(), Server: server.URL, MinPollInterval: time.Millisecond}
}

func TestDefaultServer(t *testing.T) {
	t.Setenv(consoleServerEnv, "")
	if got := DefaultServer(); got != defaultConsoleHost {
		t.Fatalf("DefaultServer = %q, want %q", got, defaultConsoleHost)
	}

	t.Setenv(consoleServerEnv, "https://console.example.com/")
	if got := DefaultServer(); got != "https://console.example.com/" {
		t.Fatalf("DefaultServer = %q, want override", got)
	}
}

func TestRequestDeviceAuth(t *testing.T) {
	var gotBody map[string]any
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/device/code" {
			t.Errorf("path = %q, want /auth/device/code", r.URL.Path)
		}
		gotUA = r.Header.Get("User-Agent")

		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-1",
			"user_code":                 "WZDG-HFHC",
			"verification_uri":          "/console/device",
			"verification_uri_complete": "/console/device?user_code=WZDG-HFHC&client_id=opencode-cli",
			"expires_in":                600,
			"interval":                  5,
		})
	}))
	defer server.Close()

	device, err := testClient(server).RequestDeviceAuth(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceAuth: %v", err)
	}
	if gotBody["client_id"] != opencodeClientID {
		t.Fatalf("client_id = %v, want %q", gotBody["client_id"], opencodeClientID)
	}
	if gotUA != UserAgent {
		t.Fatalf("User-Agent = %q, want %q", gotUA, UserAgent)
	}
	if device.DeviceCode != "dev-1" || device.UserCode != "WZDG-HFHC" {
		t.Fatalf("device = %+v, want codes dev-1/WZDG-HFHC", device)
	}
	if want := server.URL + "/console/device?user_code=WZDG-HFHC&client_id=opencode-cli"; device.CompleteURI != want {
		t.Fatalf("CompleteURI = %q, want %q", device.CompleteURI, want)
	}
	if device.Interval != 5 {
		t.Fatalf("Interval = %d, want 5", device.Interval)
	}
	if time.Until(device.ExpiresAt) < 590*time.Second {
		t.Fatalf("ExpiresAt too soon: %v", device.ExpiresAt)
	}
}

func TestResolveURI(t *testing.T) {
	absolute := resolveURI("https://opencode.ai/console", "https://example.com/x?y=1")
	if absolute != "https://example.com/x?y=1" {
		t.Fatalf("absolute = %q", absolute)
	}

	relative := resolveURI("https://opencode.ai/console", "/console/device?user_code=A")
	if relative != "https://opencode.ai/console/device?user_code=A" {
		t.Fatalf("relative = %q", relative)
	}

	if got := resolveURI("https://opencode.ai/console", ""); got != "" {
		t.Fatalf("empty = %q, want empty", got)
	}
}

func TestPollDeviceToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls == 1 && body["grant_type"] != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("grant_type = %v", body["grant_type"])
		}

		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1, 2:
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "acc-1",
				"refresh_token": "ref-1",
				"expires_in":    3600,
				"org_id":        "org-1",
			})
		}
	}))
	defer server.Close()

	device := DeviceAuth{DeviceCode: "dev-1", ExpiresAt: time.Now().Add(time.Minute)}
	token, err := testClient(server).PollDeviceToken(context.Background(), device)
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if token.AccessToken != "acc-1" || token.RefreshToken != "ref-1" || token.OrgID != "org-1" {
		t.Fatalf("token = %+v", token)
	}
	if calls != 3 {
		t.Fatalf("poll calls = %d, want 3", calls)
	}
}

func TestPollDeviceTokenDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
	}))
	defer server.Close()

	device := DeviceAuth{DeviceCode: "dev-1", ExpiresAt: time.Now().Add(time.Minute)}
	_, err := testClient(server).PollDeviceToken(context.Background(), device)
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v, want access_denied", err)
	}
}

func TestPollDeviceTokenTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer server.Close()

	device := DeviceAuth{DeviceCode: "dev-1", ExpiresAt: time.Now().Add(20 * time.Millisecond)}
	_, err := testClient(server).PollDeviceToken(context.Background(), device)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want expiry", err)
	}
}

func TestRefreshRotates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "acc-2",
			"refresh_token": "ref-2",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	token, err := testClient(server).Refresh(context.Background(), "ref-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if token.AccessToken != "acc-2" || token.RefreshToken != "ref-2" {
		t.Fatalf("token = %+v, want rotation", token)
	}
	if time.Until(token.ExpiresAt) < 3590*time.Second {
		t.Fatalf("ExpiresAt too soon: %v", token.ExpiresAt)
	}
}

func TestRefreshKeepsOldTokenWhenNotRotated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "acc-2", "expires_in": 3600})
	}))
	defer server.Close()

	token, err := testClient(server).Refresh(context.Background(), "ref-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if token.RefreshToken != "ref-1" {
		t.Fatalf("RefreshToken = %q, want fallback ref-1", token.RefreshToken)
	}
}

func TestRefreshInvalidGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	}))
	defer server.Close()

	_, err := testClient(server).Refresh(context.Background(), "ref-dead")
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v, want ErrInvalidGrant", err)
	}
}

func TestFetchIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer acc-1" {
			t.Errorf("Authorization = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "u-1", "email": "dev@example.com"})
		case "/api/orgs":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "org-b", "name": "Beta"},
				{"id": "org-a", "name": "Alpha"},
			})
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	identity, err := testClient(server).FetchIdentity(context.Background(), "acc-1", "org-b")
	if err != nil {
		t.Fatalf("FetchIdentity: %v", err)
	}
	if identity.AccountID != "u-1" || identity.Email != "dev@example.com" {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.OrgID != "org-b" || identity.OrgName != "Beta" {
		t.Fatalf("org = %+v, want scoped org-b", identity)
	}

	fallback, err := testClient(server).FetchIdentity(context.Background(), "acc-1", "")
	if err != nil {
		t.Fatalf("FetchIdentity fallback: %v", err)
	}
	if fallback.OrgID != "org-a" || fallback.OrgName != "Alpha" {
		t.Fatalf("fallback org = %+v, want first by name", fallback)
	}

	if _, err := testClient(server).FetchIdentity(context.Background(), "acc-1", "org-missing"); err == nil {
		t.Fatal("missing scoped org: want error")
	}
}
