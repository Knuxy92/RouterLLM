package alysis

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
	// SupabaseURL and AnonKey ship inside the upstream alysis CLI; the anon key
	// is public by design and only gates anonymous device-flow access.
	SupabaseURL = "https://vzigujbcjjmpntxhmyvr.supabase.co"
	SiteURL     = "https://alysiscode.com"
	AnonKey     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InZ6aWd1amJjamptcG50eGhteXZyIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODA5Mzc0NTIsImV4cCI6MjA5NjUxMzQ1Mn0.vLH9q-BNO8IWIZrVlvCw8pZWXdLgmKG4Tl9toTTD3pg"
)

const (
	maxClientNameLength = 80
	defaultExpiresIn    = 900
	maxPollDuration     = 15 * time.Minute
	minPollInterval     = 5 * time.Second
)

type Endpoints struct {
	DeviceCode  string
	DeviceToken string
	Models      string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		DeviceCode:  SupabaseURL + "/functions/v1/device-code",
		DeviceToken: SupabaseURL + "/functions/v1/device-token",
		Models:      SupabaseURL + "/functions/v1/llm/v1/models",
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

type Client struct {
	HTTPClient *http.Client
	Endpoints  Endpoints

	// minPollInterval overrides the 5s device-poll floor when set (test hook).
	minPollInterval time.Duration
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
	if e.Models == "" {
		e.Models = defaults.Models
	}

	return e
}

func (c *Client) RequestDeviceAuth(ctx context.Context, clientName string) (DeviceAuth, error) {
	if runes := []rune(clientName); len(runes) > maxClientNameLength {
		clientName = string(runes[:maxClientNameLength])
	}

	resp, err := c.postJSON(ctx, c.endpoints().DeviceCode, map[string]string{"client_name": clientName})
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("alysis device auth returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var device DeviceAuth
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode device auth: %w", err)
	}
	if device.DeviceCode == "" {
		return DeviceAuth{}, fmt.Errorf("alysis device auth returned an empty device code")
	}

	return device, nil
}

type deviceTokenResponse struct {
	Status string `json:"status"`
	Key    string `json:"key"`
}

func (c *Client) PollForToken(ctx context.Context, device DeviceAuth) (string, error) {
	minInterval := c.minPollInterval
	if minInterval <= 0 {
		minInterval = minPollInterval
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval < minInterval {
		interval = minInterval
	}

	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultExpiresIn
	}
	deadline := time.Now().Add(min(maxPollDuration, time.Duration(expiresIn)*time.Second))

	for time.Now().Before(deadline) {
		resp, err := c.postJSON(ctx, c.endpoints().DeviceToken, map[string]string{"device_code": device.DeviceCode})
		if err != nil {
			return "", err
		}

		body := readLimited(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("alysis device token returned %s: %s", resp.Status, body)
		}

		var token deviceTokenResponse
		if err := json.Unmarshal([]byte(body), &token); err != nil {
			return "", fmt.Errorf("decode device token: %w", err)
		}

		switch token.Status {
		case "approved":
			if token.Key == "" {
				return "", fmt.Errorf("alysis device login returned an empty gateway key")
			}

			return token.Key, nil
		case "denied":
			return "", fmt.Errorf("alysis login was rejected on the website")
		case "expired", "not_found", "already_claimed":
			return "", fmt.Errorf("alysis login code expired — run again")
		}

		if err := sleepContext(ctx, interval); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("alysis device login expired before authorization completed")
}

func (c *Client) ActivationURL(device DeviceAuth) string {
	if pinned := pinnedActivationURL(device); pinned != "" {
		return pinned
	}

	return SiteURL + "/activate?code=" + url.QueryEscape(device.UserCode)
}

// pinnedActivationURL keeps the path and query of the server-returned URL but
// always swaps the host for alysiscode.com, mirroring the official CLI.
func pinnedActivationURL(device DeviceAuth) string {
	source := device.VerificationURIComplete
	if source == "" {
		source = device.VerificationURI
	}
	if source == "" {
		return ""
	}

	parsed, err := url.Parse(source)
	if err != nil || parsed.Path == "" {
		return ""
	}

	site, err := url.Parse(SiteURL)
	if err != nil {
		return ""
	}
	pinned := *parsed
	pinned.Scheme = site.Scheme
	pinned.Host = site.Host
	pinned.User = nil

	return pinned.String()
}

func (c *Client) VerifyKey(ctx context.Context, key string) ([]string, error) {
	endpoint := c.endpoints().Models

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alysis models returned %s: %s", resp.Status, readLimited(resp.Body))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode alysis models: %w", err)
	}

	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		models = append(models, model.ID)
	}

	return models, nil
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
	req.Header.Set("apikey", AnonKey)
	req.Header.Set("Authorization", "Bearer "+AnonKey)

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
