package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"routerllm/internal/util"
)

const (
	opencodeClientID   = "opencode-cli"
	defaultConsoleHost = "https://opencode.ai/console"
	consoleServerEnv   = "OPENCODE_CONSOLE_URL"
	defaultDeviceTTL   = 600
	defaultTokenTTL    = 3600
	tokenSkew          = time.Minute

	// pollIntervalFloor is the slowest cadence accepted for device polling.
	pollIntervalFloor = 5 * time.Second
	// slowDownBump adds to the poll interval after a slow_down response.
	slowDownBump = 5 * time.Second
)

// ErrInvalidGrant is returned when the Console rejects a refresh token. The
// session behind it was revoked or expired; only a new device login recovers.
var ErrInvalidGrant = errors.New("opencode refresh token is no longer valid (re-login required)")

// DefaultServer returns the Console server for the device flow, overridable
// with OPENCODE_CONSOLE_URL for self-hosted consoles.
func DefaultServer() string {
	if server := strings.TrimSpace(os.Getenv(consoleServerEnv)); server != "" {
		return server
	}

	return defaultConsoleHost
}

type Client struct {
	HTTPClient      *http.Client
	Server          string
	MinPollInterval time.Duration
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return http.DefaultClient
}

func (c *Client) server() string {
	server := strings.TrimSpace(c.Server)
	if server == "" {
		server = DefaultServer()
	}

	return strings.TrimRight(server, "/")
}

type DeviceAuth struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	CompleteURI     string
	Interval        int
	ExpiresAt       time.Time
}

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	OrgID        string
}

type Identity struct {
	AccountID string
	Email     string
	OrgID     string
	OrgName   string
}

type deviceCodeResponse struct {
	DeviceCode              string  `json:"device_code"`
	UserCode                string  `json:"user_code"`
	VerificationURI         string  `json:"verification_uri"`
	VerificationURIComplete string  `json:"verification_uri_complete"`
	ExpiresIn               flexInt `json:"expires_in"`
	Interval                flexInt `json:"interval"`
}

type deviceTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    flexInt `json:"expires_in"`
	OrgID        string `json:"org_id"`
	Error        string `json:"error"`
}

type userResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type orgResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// flexInt reads a numeric field the Console may send as a JSON number or a
// quoted string.
type flexInt int

func (f *flexInt) UnmarshalJSON(raw []byte) error {
	value, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err != nil || value < 0 {
		return fmt.Errorf("invalid integer %s", strings.TrimSpace(string(raw)))
	}
	*f = flexInt(value)

	return nil
}

func (c *Client) RequestDeviceAuth(ctx context.Context) (DeviceAuth, error) {
	resp, err := c.postJSON(ctx, c.server()+"/auth/device/code", map[string]any{
		"client_id":          opencodeClientID,
		"supports_org_scope": true,
	})
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("opencode device auth returned %s: %s", resp.Status, util.ReadLimited(resp.Body))
	}

	var payload deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode opencode device auth: %w", err)
	}
	if payload.DeviceCode == "" || payload.UserCode == "" {
		return DeviceAuth{}, fmt.Errorf("opencode device auth returned an empty device code or user code")
	}

	device := DeviceAuth{
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURI: payload.VerificationURI,
		CompleteURI:     resolveURI(c.server(), payload.VerificationURIComplete),
		Interval:        int(payload.Interval),
		ExpiresAt:       time.Now().Add(expiresInDuration(int(payload.ExpiresIn), defaultDeviceTTL)),
	}
	if device.CompleteURI == "" {
		device.CompleteURI = device.VerificationURI
	}

	return device, nil
}

// resolveURI turns the Console's relative verification path into an absolute
// browser URL. An absolute value passes through; anything unparsable yields "".
func resolveURI(server, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
		return parsed.String()
	}

	base, err := url.Parse(server + "/")
	if err != nil {
		return ""
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return base.ResolveReference(ref).String()
}

func (c *Client) PollDeviceToken(ctx context.Context, device DeviceAuth) (Token, error) {
	interval := c.pollInterval(device)
	deadline := time.Now().Add(deviceLifetime(device))

	for time.Now().Before(deadline) {
		resp, err := c.postJSON(ctx, c.server()+"/auth/device/token", map[string]any{
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			"device_code": device.DeviceCode,
			"client_id":   opencodeClientID,
		})
		if err != nil {
			return Token{}, err
		}

		var payload deviceTokenResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		status := resp.StatusCode
		resp.Body.Close()

		if status == http.StatusOK && decodeErr == nil && payload.AccessToken != "" {
			return tokenFromResponse(payload, ""), nil
		}

		switch payload.Error {
		case "authorization_pending":
		case "slow_down":
			interval += slowDownBump
		default:
			if decodeErr != nil {
				return Token{}, fmt.Errorf("decode opencode device token: %w", decodeErr)
			}
			if payload.Error != "" {
				return Token{}, fmt.Errorf("opencode device login failed: %s", payload.Error)
			}

			return Token{}, fmt.Errorf("opencode device login failed: %s", resp.Status)
		}

		if err := util.SleepContext(ctx, interval); err != nil {
			return Token{}, err
		}
	}

	return Token{}, fmt.Errorf("opencode device login expired before authorization completed")
}

func (c *Client) pollInterval(device DeviceAuth) time.Duration {
	minInterval := c.MinPollInterval
	if minInterval <= 0 {
		minInterval = pollIntervalFloor
	}

	interval := time.Duration(device.Interval) * time.Second
	if interval < minInterval {
		interval = minInterval
	}

	return interval
}

func deviceLifetime(device DeviceAuth) time.Duration {
	if !device.ExpiresAt.IsZero() {
		return time.Until(device.ExpiresAt)
	}

	return time.Duration(defaultDeviceTTL) * time.Second
}

// FetchIdentity reads the account and org list behind an access token,
// selecting the org the token is scoped to and falling back to the first org
// by name. A non-empty orgID with no match is a hard error.
func (c *Client) FetchIdentity(ctx context.Context, accessToken, orgID string) (Identity, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return Identity{}, fmt.Errorf("opencode access token is empty")
	}

	var user userResponse
	if err := c.getJSON(ctx, c.server()+"/api/user", accessToken, &user); err != nil {
		return Identity{}, err
	}
	if user.ID == "" {
		return Identity{}, fmt.Errorf("opencode console returned an empty user id")
	}

	var orgs []orgResponse
	if err := c.getJSON(ctx, c.server()+"/api/orgs", accessToken, &orgs); err != nil {
		return Identity{}, err
	}

	identity := Identity{AccountID: user.ID, Email: user.Email}

	org := pickOrg(orgs, orgID)
	if org == nil {
		if orgID != "" {
			return Identity{}, fmt.Errorf("opencode organization not found: %s", orgID)
		}

		return Identity{}, fmt.Errorf("opencode console returned no organizations")
	}
	identity.OrgID, identity.OrgName = org.ID, org.Name

	return identity, nil
}

// pickOrg selects the org matching orgID, falling back to the first org by
// (name, id). A non-empty orgID with no match is an error the caller surfaces.
func pickOrg(orgs []orgResponse, orgID string) *orgResponse {
	if len(orgs) == 0 {
		return nil
	}

	sorted := make([]orgResponse, len(orgs))
	copy(sorted, orgs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}

		return sorted[i].ID < sorted[j].ID
	})

	if orgID != "" {
		for i := range sorted {
			if sorted[i].ID == orgID {
				return &sorted[i]
			}
		}

		return nil
	}

	return &sorted[0]
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, fmt.Errorf("opencode refresh token is empty")
	}

	resp, err := c.postJSON(ctx, c.server()+"/auth/device/token", map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     opencodeClientID,
	})
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		var denied deviceTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&denied); err == nil && denied.Error == "invalid_grant" {
			return Token{}, ErrInvalidGrant
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("opencode refresh returned %s: %s", resp.Status, util.ReadLimited(resp.Body))
	}

	var payload deviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Token{}, fmt.Errorf("decode opencode refresh: %w", err)
	}
	if payload.AccessToken == "" {
		return Token{}, fmt.Errorf("opencode refresh returned an empty access token")
	}

	return tokenFromResponse(payload, refreshToken), nil
}

// tokenFromResponse keeps the old refresh token when the server does not
// rotate it, so callers always get a usable refresh token back.
func tokenFromResponse(payload deviceTokenResponse, fallbackRefresh string) Token {
	refreshToken := strings.TrimSpace(payload.RefreshToken)
	if refreshToken == "" {
		refreshToken = fallbackRefresh
	}

	return Token{
		AccessToken:  payload.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(expiresInDuration(int(payload.ExpiresIn), defaultTokenTTL)),
		OrgID:        strings.TrimSpace(payload.OrgID),
	}
}

func expiresInDuration(seconds, fallback int) time.Duration {
	if seconds <= 0 {
		seconds = fallback
	}

	return time.Duration(seconds) * time.Second
}

func (c *Client) postJSON(ctx context.Context, endpoint string, payload any) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}

	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint, accessToken string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode console %s returned %s: %s", endpoint, resp.Status, util.ReadLimited(resp.Body))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode opencode console %s: %w", endpoint, err)
	}

	return nil
}
