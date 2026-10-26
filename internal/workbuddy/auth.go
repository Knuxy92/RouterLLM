package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultTokenTTL = time.Hour

// ErrInvalidGrant is returned when the gateway rejects a refresh token. The
// session behind it was revoked or expired; only a new login recovers.
var ErrInvalidGrant = errors.New("workbuddy refresh token is no longer valid (re-login required)")

type Client struct {
	HTTPClient *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return http.DefaultClient
}

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type authResponse struct {
	Code flexInt         `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type authPayload struct {
	AccessToken  string  `json:"accessToken"`
	RefreshToken string  `json:"refreshToken"`
	ExpiresAt    flexInt `json:"expiresAt"`
}

type authState struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

// flexInt reads a numeric field the gateway may send as a JSON number or a
// quoted string; a JSON null reads as zero.
type flexInt int64

func (f *flexInt) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(string(raw), `"`)
	if text == "" || text == "null" {
		*f = 0

		return nil
	}

	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 {
		return fmt.Errorf("invalid integer %s", strings.TrimSpace(string(raw)))
	}
	*f = flexInt(value)

	return nil
}

// Refresh exchanges a refresh token for a fresh access token at the realm's
// plugin auth endpoint. The gateway rotates refresh tokens, so the returned
// token carries the newest one when the response includes it.
func (c *Client) Refresh(ctx context.Context, realm, uid, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, fmt.Errorf("workbuddy refresh token is empty")
	}

	hosts := Hosts(realm)
	body, status, err := c.callAuth(ctx, http.MethodPost, hosts.ChatBase+RefreshPath, realm, "{}", map[string]string{
		"X-Refresh-Token":       refreshToken,
		"X-Auth-Refresh-Source": refreshSource(realm),
		"X-User-Id":             uid,
	})
	if err != nil {
		return Token{}, err
	}

	var payload authResponse
	parseErr := json.Unmarshal(body, &payload)
	if parseErr == nil && isAuthCode(int(payload.Code)) {
		return Token{}, fmt.Errorf("%w: %s", ErrInvalidGrant, excerpt(body))
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return Token{}, fmt.Errorf("%w: %s", ErrInvalidGrant, excerpt(body))
	}
	if parseErr != nil {
		return Token{}, fmt.Errorf("decode workbuddy refresh: %w", parseErr)
	}
	if status < 200 || status > 299 {
		return Token{}, fmt.Errorf("workbuddy refresh returned %d: %s", status, excerpt(body))
	}
	if payload.Code != 0 {
		return Token{}, fmt.Errorf("workbuddy refresh failed (code %d): %s", int(payload.Code), payload.Msg)
	}

	claims, err := decodeAuthClaims(payload.Data)
	if err != nil {
		return Token{}, fmt.Errorf("decode workbuddy refresh: %w", err)
	}
	if claims.AccessToken == "" {
		return Token{}, fmt.Errorf("workbuddy refresh returned an empty access token")
	}

	token := Token{
		AccessToken:  claims.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    normalizeExpiry(int64(claims.ExpiresAt)),
	}
	if claims.RefreshToken != "" {
		token.RefreshToken = claims.RefreshToken
	}
	if token.ExpiresAt.IsZero() {
		token.ExpiresAt = time.Now().Add(defaultTokenTTL)
	}

	return token, nil
}

// StartLogin opens a browser OAuth login: show authURL to the user and poll
// PollLogin with the returned state until it completes.
func (c *Client) StartLogin(ctx context.Context, realm, platform string) (string, string, error) {
	hosts := Hosts(realm)
	endpoint := hosts.ChatBase + AuthStatePath + "?platform=" + url.QueryEscape(NormalizeProduct(platform))

	body, status, err := c.callAuth(ctx, http.MethodPost, endpoint, realm, "{}", nil)
	if err != nil {
		return "", "", err
	}

	var payload authResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("decode workbuddy auth state: %w", err)
	}
	if status < 200 || status > 299 {
		return "", "", fmt.Errorf("workbuddy auth state returned %d: %s", status, excerpt(body))
	}
	if payload.Code != 0 {
		return "", "", fmt.Errorf("workbuddy auth state failed (code %d): %s", int(payload.Code), payload.Msg)
	}

	state, err := decodeAuthState(payload.Data)
	if err != nil {
		return "", "", fmt.Errorf("decode workbuddy auth state: %w", err)
	}
	if state.State == "" {
		return "", "", fmt.Errorf("workbuddy auth state returned an empty state")
	}

	return state.State, state.AuthURL, nil
}

// PollLogin checks one browser login attempt. pending is true while the user
// has not finished approving — any response without an access token, including
// the gateway's "login ing" code.
func (c *Client) PollLogin(ctx context.Context, realm, state string) (Token, bool, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return Token{}, false, fmt.Errorf("workbuddy login state is empty")
	}

	hosts := Hosts(realm)
	body, status, err := c.callAuth(ctx, http.MethodGet, hosts.ChatBase+AuthTokenPath+"?state="+url.QueryEscape(state), realm, "", nil)
	if err != nil {
		return Token{}, false, err
	}

	var payload authResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		if status < 200 || status > 299 {
			return Token{}, false, fmt.Errorf("workbuddy login poll returned %d: %s", status, excerpt(body))
		}

		return Token{}, false, fmt.Errorf("decode workbuddy login poll: %w", err)
	}

	claims, err := decodeAuthClaims(payload.Data)
	if err != nil {
		return Token{}, false, fmt.Errorf("decode workbuddy login poll: %w", err)
	}
	if claims.AccessToken == "" {
		return Token{}, true, nil
	}

	token := Token{
		AccessToken:  claims.AccessToken,
		RefreshToken: claims.RefreshToken,
		ExpiresAt:    normalizeExpiry(int64(claims.ExpiresAt)),
	}
	if token.ExpiresAt.IsZero() {
		token.ExpiresAt = time.Now().Add(defaultTokenTTL)
	}

	return token, false, nil
}

// newRequest builds a request with the desktop identity header family shared
// by every auth call.
func (c *Client) newRequest(ctx context.Context, method, endpoint, realm, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	hosts := Hosts(realm)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", DesktopUA(realm))
	req.Header.Set("Origin", hosts.Origin)
	req.Header.Set("Referer", hosts.Origin+"/")
	req.Header.Set("X-Domain", hosts.Domain)
	req.Header.Set("X-CodeBuddy-Request", "1")
	req.Header.Set("Accept-Language", acceptLanguage(realm))

	return req, nil
}

// callAuth performs one round trip against the realm's auth endpoints with the
// shared identity header family, plus any token headers the caller adds.
func (c *Client) callAuth(ctx context.Context, method, endpoint, realm, body string, tokenHeaders map[string]string) ([]byte, int, error) {
	req, err := c.newRequest(ctx, method, endpoint, realm, body)
	if err != nil {
		return nil, 0, err
	}
	for key, value := range tokenHeaders {
		req.Header.Set(key, value)
	}

	return c.doAuth(req)
}

// doAuth sends one auth request and returns the raw body and HTTP status.
func (c *Client) doAuth(req *http.Request) ([]byte, int, error) {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s: %w", req.URL.Path, err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("read %s response: %w", req.URL.Path, err)
	}

	return body, resp.StatusCode, nil
}

func refreshSource(realm string) string {
	if NormalizeRealm(realm) == "cn" {
		return "workbuddy"
	}

	return "plugin"
}

func acceptLanguage(realm string) string {
	if NormalizeRealm(realm) == "cn" {
		return "zh-CN"
	}

	return "en-US"
}

// isAuthCode reports whether a gateway code marks the credential unusable: an
// explicit 401/403 or the 11xxx auth error band.
func isAuthCode(code int) bool {
	return code == 401 || code == 403 || (code >= 11000 && code <= 11999)
}

// normalizeExpiry converts an expiresAt stamp sent in seconds or milliseconds
// (values above 1e12) into an absolute time.
func normalizeExpiry(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 1_000_000_000_000 {
		value /= 1000
	}

	return time.Unix(value, 0)
}

// unwrapData follows a nested data object when the gateway double-wraps its
// payload as {"data":{"data":{...}}}.
func unwrapData(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || raw[0] != '{' {
		return raw
	}

	var nested struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &nested); err != nil || len(nested.Data) == 0 || nested.Data[0] != '{' {
		return raw
	}

	return nested.Data
}

// decodeObject unmarshals raw into target when raw holds a JSON object;
// anything else (null, a string, an array) leaves target untouched.
func decodeObject(raw json.RawMessage, target any) error {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}

	return json.Unmarshal(raw, target)
}

func decodeAuthClaims(data json.RawMessage) (authPayload, error) {
	var claims authPayload
	if err := decodeObject(unwrapData(data), &claims); err != nil {
		return authPayload{}, err
	}

	return claims, nil
}

func decodeAuthState(data json.RawMessage) (authState, error) {
	var parsed authState
	if err := decodeObject(unwrapData(data), &parsed); err != nil {
		return authState{}, err
	}

	return parsed, nil
}

// excerpt trims a response body for an error message.
func excerpt(body []byte) string {
	raw := strings.TrimSpace(string(body))
	if len(raw) > 200 {
		raw = raw[:200]
	}

	return raw
}
