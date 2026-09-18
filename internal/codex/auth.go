package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexVersion      = "26.915.31029"
	codexOriginator   = "codex_cli_rs"
	codexUserAgent    = codexOriginator + "/" + codexVersion + " (linux x64)"
	codexBetaHeader   = "responses_websockets=2026-02-06"
	oauthScope        = "openid profile email offline_access"
	deviceCallbackURI = "https://auth.openai.com/deviceauth/callback"
	deviceLoginURL    = "https://auth.openai.com/codex/device"
	authClaimKey      = "https://api.openai.com/auth"
	profileClaimKey   = "https://api.openai.com/profile"
	accountIDClaim    = "chatgpt_account_id"
	defaultTokenTTL   = 3600
	defaultDeviceTTL  = 900
	tokenSkew         = time.Minute
)

type Endpoints struct {
	DeviceCode  string
	DeviceToken string
	Token       string
}

// DefaultEndpoints are the Codex CLI's own device-auth endpoints. The RFC 8628
// pair (`/oauth/device/code` + `urn:ietf:params:oauth:grant-type:device_code`)
// lives behind a Cloudflare challenge that blocks non-browser clients, so the
// login must use the native endpoints the official CLI uses.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		DeviceCode:  "https://auth.openai.com/api/accounts/deviceauth/usercode",
		DeviceToken: "https://auth.openai.com/api/accounts/deviceauth/token",
		Token:       "https://auth.openai.com/oauth/token",
	}
}

type DeviceAuth struct {
	DeviceAuthID            string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                int
	ExpiresAt               time.Time
}

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type Client struct {
	HTTPClient      *http.Client
	Endpoints       Endpoints
	MinPollInterval time.Duration
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return http.DefaultClient
}

func (c *Client) endpoints() Endpoints {
	defaults := DefaultEndpoints()
	e := c.Endpoints
	if e.DeviceCode == "" {
		e.DeviceCode = defaults.DeviceCode
	}
	if e.DeviceToken == "" {
		e.DeviceToken = defaults.DeviceToken
	}
	if e.Token == "" {
		e.Token = defaults.Token
	}

	return e
}

type deviceAuthResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	Interval     json.RawMessage `json:"interval"`
	ExpiresAt    string          `json:"expires_at"`
}

func (c *Client) RequestDeviceAuth(ctx context.Context) (DeviceAuth, error) {
	resp, err := c.postJSON(ctx, c.endpoints().DeviceCode, map[string]string{"client_id": codexClientID})
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("codex device auth returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var payload deviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode codex device auth: %w", err)
	}
	if payload.DeviceAuthID == "" || payload.UserCode == "" {
		return DeviceAuth{}, fmt.Errorf("codex device auth returned an empty device id or user code")
	}

	device := DeviceAuth{
		DeviceAuthID:    payload.DeviceAuthID,
		UserCode:        payload.UserCode,
		VerificationURI: deviceLoginURL,
		Interval:        flexInt(payload.Interval),
	}
	if expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt); err == nil {
		device.ExpiresAt = expiresAt
	}

	return device, nil
}

// flexInt reads a numeric field that the auth server may send as either a JSON
// number or a quoted string ("interval": "5").
func flexInt(raw json.RawMessage) int {
	value, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err != nil || value < 0 {
		return 0
	}

	return value
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	ExpiresIn         int    `json:"expires_in"`
	Error             struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (c *Client) PollDeviceToken(ctx context.Context, device DeviceAuth) (Token, error) {
	interval := c.pollInterval(device)
	deadline := time.Now().Add(deviceLifetime(device))

	for time.Now().Before(deadline) {
		resp, err := c.postJSON(ctx, c.endpoints().DeviceToken, map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return Token{}, err
		}

		var payload deviceTokenResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		status := resp.StatusCode
		resp.Body.Close()

		if status == http.StatusOK {
			if payload.AuthorizationCode != "" {
				return c.exchangeDeviceCode(ctx, payload.AuthorizationCode, payload.CodeVerifier)
			}
			if payload.AccessToken != "" {
				return tokenFromResponse(tokenResponse{
					AccessToken:  payload.AccessToken,
					RefreshToken: payload.RefreshToken,
					ExpiresIn:    payload.ExpiresIn,
				}, ""), nil
			}
		}

		switch payload.Error.Code {
		case "deviceauth_authorization_pending":
		case "deviceauth_slow_down":
			interval += 5 * time.Second
		default:
			if decodeErr != nil {
				return Token{}, fmt.Errorf("decode codex device token: %w", decodeErr)
			}

			return Token{}, fmt.Errorf("codex device login failed: %s", deviceErrorMessage(payload, resp.Status))
		}

		if err := sleepContext(ctx, interval); err != nil {
			return Token{}, err
		}
	}

	return Token{}, fmt.Errorf("codex device login expired before authorization completed")
}

// exchangeDeviceCode trades the authorization code the device poll returns for
// real tokens. The redirect URI is the fixed callback the Codex CLI registers.
func (c *Client) exchangeDeviceCode(ctx context.Context, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {deviceCallbackURI},
	}
	resp, err := c.postForm(ctx, c.endpoints().Token, form)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("codex device token exchange returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var payload tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Token{}, fmt.Errorf("decode codex device token exchange: %w", err)
	}
	if payload.AccessToken == "" {
		return Token{}, fmt.Errorf("codex device token exchange returned an empty access token")
	}

	return tokenFromResponse(payload, ""), nil
}

func (c *Client) pollInterval(device DeviceAuth) time.Duration {
	minInterval := c.MinPollInterval
	if minInterval <= 0 {
		minInterval = 5 * time.Second
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

func deviceErrorMessage(payload deviceTokenResponse, status string) string {
	if payload.Error.Message != "" {
		return payload.Error.Message
	}
	if payload.Error.Code != "" {
		return payload.Error.Code
	}

	return status
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, fmt.Errorf("codex refresh token is empty")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {codexClientID},
		"refresh_token": {refreshToken},
		"scope":         {oauthScope},
	}
	resp, err := c.postForm(ctx, c.endpoints().Token, form)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("codex refresh returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var payload tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Token{}, fmt.Errorf("decode codex refresh: %w", err)
	}
	if payload.AccessToken == "" {
		return Token{}, fmt.Errorf("codex refresh returned an empty access token")
	}

	return tokenFromResponse(payload, refreshToken), nil
}

// tokenFromResponse keeps the old refresh token when the server does not rotate
// it, so callers always get a usable refresh token back.
func tokenFromResponse(payload tokenResponse, fallbackRefresh string) Token {
	refreshToken := strings.TrimSpace(payload.RefreshToken)
	if refreshToken == "" {
		refreshToken = fallbackRefresh
	}

	details := inspectAccessToken(payload.AccessToken, payload.ExpiresIn)

	return Token{
		AccessToken:  payload.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    details.ExpiresAt,
	}
}

type tokenDetails struct {
	AccountID string
	Email     string
	ExpiresAt time.Time
}

// inspectAccessToken reads identity and expiry from the access token without
// verifying its signature: the token comes straight from the auth server over
// TLS, and the same claims are what the upstream fingerprints.
func inspectAccessToken(accessToken string, expiresIn int) tokenDetails {
	details := tokenDetails{ExpiresAt: time.Now().Add(expiresInDuration(expiresIn))}

	claims, err := decodeJWTClaims(accessToken)
	if err != nil {
		return details
	}

	if exp, ok := numericClaim(claims, "exp"); ok {
		details.ExpiresAt = time.Unix(int64(exp), 0)
	}
	if auth, ok := claims[authClaimKey].(map[string]any); ok {
		details.AccountID, _ = auth[accountIDClaim].(string)
	}
	if profile, ok := claims[profileClaimKey].(map[string]any); ok {
		details.Email, _ = profile["email"].(string)
	}
	if details.Email == "" {
		details.Email, _ = claims["email"].(string)
	}

	return details
}

func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("token is not a JWT")
	}

	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", err)
	}

	return claims, nil
}

func numericClaim(claims map[string]any, key string) (float64, bool) {
	value, ok := claims[key].(float64)

	return value, ok
}

func expiresInDuration(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = defaultTokenTTL
	}

	return time.Duration(seconds) * time.Second
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}

	return resp, nil
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
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}

	return resp, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readLimited(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 2048))

	return string(raw)
}

func SetHeaders(header http.Header, accessToken string) {
	header.Set("Authorization", "Bearer "+accessToken)
	header.Set("originator", codexOriginator)
	header.Set("User-Agent", codexUserAgent)
	header.Set("Content-Type", "application/json")
	header.Set("OpenAI-Beta", codexBetaHeader)
	header.Set("Accept", "text/event-stream")

	if accountID := inspectAccessToken(accessToken, 0).AccountID; accountID != "" {
		header.Set("ChatGPT-Account-Id", accountID)
	}
}
