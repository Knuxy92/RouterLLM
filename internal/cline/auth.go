package cline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	WorkOSClientID = "client_01K3A541FN8TA3EPPHTD2325AR"
	tokenSkew      = time.Minute
)

type Endpoints struct {
	DeviceAuth   string
	Authenticate string
	Register     string
	Refresh      string
	Completions  string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		DeviceAuth:   "https://api.workos.com/user_management/authorize/device",
		Authenticate: "https://api.workos.com/user_management/authenticate",
		Register:     "https://api.cline.bot/api/v1/auth/register",
		Refresh:      "https://api.cline.bot/api/v1/auth/refresh",
		Completions:  "https://api.cline.bot/api/v1/chat/completions",
	}
}

type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
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
	if e.DeviceAuth == "" {
		e.DeviceAuth = defaults.DeviceAuth
	}
	if e.Authenticate == "" {
		e.Authenticate = defaults.Authenticate
	}
	if e.Register == "" {
		e.Register = defaults.Register
	}
	if e.Refresh == "" {
		e.Refresh = defaults.Refresh
	}
	if e.Completions == "" {
		e.Completions = defaults.Completions
	}

	return e
}

func (c *Client) RequestDeviceAuth(ctx context.Context) (DeviceAuth, error) {
	form := url.Values{"client_id": {WorkOSClientID}}
	resp, err := c.postForm(ctx, c.endpoints().DeviceAuth, form)
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("workos device auth returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var device DeviceAuth
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode device auth: %w", err)
	}
	if device.DeviceCode == "" {
		return DeviceAuth{}, fmt.Errorf("workos device auth returned an empty device code")
	}

	return device, nil
}

type workosToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (c *Client) PollDeviceToken(ctx context.Context, device DeviceAuth) (string, string, error) {
	minInterval := c.MinPollInterval
	if minInterval <= 0 {
		minInterval = 5 * time.Second
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval < minInterval {
		interval = minInterval
	}
	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for time.Now().Before(deadline) {
		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {device.DeviceCode},
			"client_id":   {WorkOSClientID},
		}
		resp, err := c.postForm(ctx, c.endpoints().Authenticate, form)
		if err != nil {
			return "", "", err
		}

		var token workosToken
		decodeErr := json.NewDecoder(resp.Body).Decode(&token)
		status := resp.StatusCode
		resp.Body.Close()
		if decodeErr != nil {
			return "", "", fmt.Errorf("decode device token: %w", decodeErr)
		}

		if status == http.StatusOK && token.AccessToken != "" {
			return token.AccessToken, token.RefreshToken, nil
		}

		switch token.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		default:
			message := token.ErrorDesc
			if message == "" {
				message = token.Error
			}
			if message == "" {
				message = resp.Status
			}

			return "", "", fmt.Errorf("workos device login failed: %s", message)
		}

		if err := sleepContext(ctx, interval); err != nil {
			return "", "", err
		}
	}

	return "", "", fmt.Errorf("workos device login expired before authorization completed")
}

type clineAuthResponse struct {
	Data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    any    `json:"expiresAt"`
		UserInfo     *struct {
			Email string `json:"email"`
		} `json:"userInfo"`
	} `json:"data"`
}

func (c *Client) Register(ctx context.Context, workosAccess, workosRefresh string) (Token, string, error) {
	payload := map[string]string{
		"accessToken":  workosAccess,
		"refreshToken": workosRefresh,
	}
	resp, err := c.postJSON(ctx, c.endpoints().Register, payload)
	if err != nil {
		return Token{}, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Token{}, "", fmt.Errorf("cline register returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var parsed clineAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Token{}, "", fmt.Errorf("decode cline register: %w", err)
	}
	if parsed.Data.RefreshToken == "" {
		return Token{}, "", fmt.Errorf("cline register returned an empty refresh token")
	}

	email := ""
	if parsed.Data.UserInfo != nil {
		email = parsed.Data.UserInfo.Email
	}

	return Token{
		AccessToken:  prefixAccessToken(parsed.Data.AccessToken),
		RefreshToken: parsed.Data.RefreshToken,
		ExpiresAt:    parseExpiry(parsed.Data.ExpiresAt),
	}, email, nil
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, fmt.Errorf("cline refresh token is empty")
	}

	payload := map[string]string{
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	}
	resp, err := c.postJSON(ctx, c.endpoints().Refresh, payload)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("cline refresh returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var parsed clineAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Token{}, fmt.Errorf("decode cline refresh: %w", err)
	}
	if parsed.Data.AccessToken == "" {
		return Token{}, fmt.Errorf("cline refresh returned an empty access token")
	}

	rotated := parsed.Data.RefreshToken
	if rotated == "" {
		rotated = refreshToken
	}

	return Token{
		AccessToken:  prefixAccessToken(parsed.Data.AccessToken),
		RefreshToken: rotated,
		ExpiresAt:    parseExpiry(parsed.Data.ExpiresAt),
	}, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", clientUserAgent)

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
	req.Header.Set("User-Agent", clientUserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}

	return resp, nil
}

func prefixAccessToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" || strings.HasPrefix(token, "workos:") {
		return token
	}

	return "workos:" + token
}

func parseExpiry(value any) time.Time {
	switch v := value.(type) {
	case float64:
		return time.UnixMilli(int64(v))
	case int64:
		return time.UnixMilli(v)
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed
			}
		}
	}

	return time.Time{}
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
