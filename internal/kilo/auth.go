package kilo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"routerllm/internal/util"
)

const (
	defaultAPIBase     = "https://api.kilo.ai"
	defaultGatewayBase = "https://ai-gateway.kilo.ai/api"
	devicePollInterval = 3 * time.Second
	defaultExpiresIn   = 600
)

// OrgHeader is the per-account organization header the gateway reads to bill
// a request to an organization instead of the personal account.
const OrgHeader = "X-KILOCODE-ORGANIZATIONID"

// OrgHeaderLower is the canonical lowercase form used in header deny-lists.
const OrgHeaderLower = "x-kilocode-organizationid"

type DeviceAuth struct {
	Code            string `json:"code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"`
}

type Profile struct {
	User struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	SelectedOrganizationID string `json:"selectedOrganizationId"`
}

type Client struct {
	HTTPClient  *http.Client
	APIBase     string
	GatewayBase string

	// pollInterval overrides the 3s device-poll cadence when set (test hook).
	pollInterval time.Duration
}

func (c *Client) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	if fromEnv := os.Getenv("KILO_API_URL"); fromEnv != "" {
		return strings.TrimRight(fromEnv, "/")
	}

	return defaultAPIBase
}

func (c *Client) gatewayBase() string {
	if c.GatewayBase != "" {
		return strings.TrimRight(c.GatewayBase, "/")
	}
	if fromEnv := os.Getenv("KILO_AI_GATEWAY_URL"); fromEnv != "" {
		return strings.TrimRight(fromEnv, "/")
	}
	if apiURL := os.Getenv("KILO_API_URL"); apiURL != "" {
		return strings.TrimRight(apiURL, "/") + "/api/gateway"
	}

	return defaultGatewayBase
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return http.DefaultClient
}

// RequestDeviceAuth starts the RFC 8628 flow. The returned Code is the short
// code used for polling.
func (c *Client) RequestDeviceAuth(ctx context.Context) (DeviceAuth, error) {
	endpoint := c.apiBase() + "/api/device-auth/codes"
	resp, err := c.postJSON(ctx, endpoint, []byte("{}"))
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	body := util.ReadLimited(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("kilo device auth returned %s: %s", resp.Status, body)
	}

	var device DeviceAuth
	if err := json.Unmarshal([]byte(body), &device); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode kilo device auth: %w", err)
	}
	if device.Code == "" {
		return DeviceAuth{}, fmt.Errorf("kilo device auth returned an empty code")
	}

	return device, nil
}

// PollForToken polls with the short code (the device_code returns 410). Kilo
// answers 202 while pending, 403 on denial, 410 on expiry and 200 once approved.
func (c *Client) PollForToken(ctx context.Context, device DeviceAuth) (string, error) {
	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultExpiresIn
	}
	interval := c.pollInterval
	if interval <= 0 {
		interval = devicePollInterval
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	pollURL := c.apiBase() + "/api/device-auth/codes/" + url.PathEscape(device.Code)

	for time.Now().Before(deadline) {
		if err := util.SleepContext(ctx, interval); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return "", fmt.Errorf("create kilo device poll request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient().Do(req)
		if err != nil {
			return "", fmt.Errorf("poll kilo device auth: %w", err)
		}
		body := util.ReadLimited(resp.Body)
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusAccepted:
			continue
		case http.StatusForbidden:
			return "", fmt.Errorf("kilo login was denied on the website")
		case http.StatusGone:
			return "", fmt.Errorf("kilo login code expired — run again")
		case http.StatusOK:
			var payload struct {
				Status    string `json:"status"`
				Token     string `json:"token"`
				UserEmail string `json:"userEmail"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				return "", fmt.Errorf("decode kilo device poll: %w", err)
			}
			if payload.Status == "approved" && payload.Token != "" {
				return payload.Token, nil
			}
		default:
			return "", fmt.Errorf("kilo device poll returned %s: %s", resp.Status, body)
		}
	}

	return "", fmt.Errorf("kilo device login expired before authorization completed")
}

// FetchProfile verifies the token and returns the account's selected org id.
func (c *Client) FetchProfile(ctx context.Context, token string) (Profile, error) {
	endpoint := c.apiBase() + "/api/profile"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Profile{}, fmt.Errorf("create kilo profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("request kilo profile: %w", err)
	}
	defer resp.Body.Close()

	body := util.ReadLimited(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Profile{}, fmt.Errorf("invalid kilo token (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return Profile{}, fmt.Errorf("kilo profile returned %s: %s", resp.Status, body)
	}

	var profile Profile
	if err := json.Unmarshal([]byte(body), &profile); err != nil {
		return Profile{}, fmt.Errorf("decode kilo profile: %w", err)
	}

	return profile, nil
}

// GatewayModelsURL returns the catalog endpoint scoped to the account's org
// when one is selected, otherwise the personal catalog.
func (c *Client) GatewayModelsURL(orgID string) string {
	base := c.gatewayBase()
	if orgID != "" {
		return base + "/v1/organizations/" + url.PathEscape(orgID) + "/models"
	}

	return base + "/v1/models"
}

func (c *Client) postJSON(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "opencode-kilo-provider")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}

	return resp, nil
}
