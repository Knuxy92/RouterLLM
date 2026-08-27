package cline

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	clientUserAgent  = "Cline/3.0.47"
	clientVersion    = "3.0.47"
	defaultModel     = "cline-free/glm-5.2"
	defaultMaxTokens = 128000
	defaultEffort    = "high"
)

type Manager struct {
	client *Client
	store  *AccountStore
	mu     sync.Mutex
	tokens map[string]Token
}

func NewManager(client *Client, store *AccountStore) *Manager {
	if client == nil {
		client = &Client{}
	}

	return &Manager{client: client, store: store, tokens: make(map[string]Token)}
}

func (m *Manager) AccessToken(ctx context.Context, refreshToken string, force bool) (string, error) {
	if !force {
		m.mu.Lock()
		cached, ok := m.tokens[refreshToken]
		m.mu.Unlock()
		if ok && cached.AccessToken != "" && time.Now().Add(tokenSkew).Before(cached.ExpiresAt) {
			return cached.AccessToken, nil
		}
	}

	token, err := m.client.Refresh(ctx, refreshToken)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.pruneExpiredLocked()
	m.tokens[refreshToken] = token
	m.mu.Unlock()

	if m.store != nil {
		if err := m.store.Rotate(refreshToken, token.RefreshToken); err != nil {
			return "", err
		}
	}

	return token.AccessToken, nil
}

// pruneExpiredLocked drops tokens that can no longer satisfy a request. Refresh
// tokens rotate and every config reload re-reads the account file, so without
// this the map keeps one entry per historical token for the process lifetime.
// Caller must hold m.mu.
func (m *Manager) pruneExpiredLocked() {
	cutoff := time.Now().Add(tokenSkew)
	for key, token := range m.tokens {
		if token.AccessToken == "" || !cutoff.Before(token.ExpiresAt) {
			delete(m.tokens, key)
		}
	}
}

func (m *Manager) Login(ctx context.Context, notify func(DeviceAuth)) (Account, error) {
	device, err := m.client.RequestDeviceAuth(ctx)
	if err != nil {
		return Account{}, err
	}
	if notify != nil {
		notify(device)
	}

	workosAccess, workosRefresh, err := m.client.PollDeviceToken(ctx, device)
	if err != nil {
		return Account{}, err
	}

	token, email, err := m.client.Register(ctx, workosAccess, workosRefresh)
	if err != nil {
		return Account{}, err
	}

	account := Account{
		AccountID:    "acc_" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		Email:        email,
		RefreshToken: token.RefreshToken,
		CreatedAt:    time.Now(),
	}
	if m.store == nil {
		return Account{}, fmt.Errorf("no cline accounts file configured")
	}
	if err := m.store.Add(account); err != nil {
		return Account{}, err
	}

	m.mu.Lock()
	m.pruneExpiredLocked()
	m.tokens[token.RefreshToken] = token
	m.mu.Unlock()

	return account, nil
}

func PrepareBody(body map[string]any) string {
	sessionID, _ := body["session_id"].(string)
	if sessionID == "" {
		sessionID = "sess_" + strconv.FormatInt(time.Now().UnixMilli(), 10)
		body["session_id"] = sessionID
	}

	if model, _ := body["model"].(string); model == "" {
		body["model"] = defaultModel
	}
	if _, ok := body["max_tokens"]; !ok {
		body["max_tokens"] = defaultMaxTokens
	}
	if effort, _ := body["reasoning_effort"].(string); effort == "" {
		body["reasoning_effort"] = defaultEffort
	}

	return sessionID
}

func SetHeaders(header http.Header, accessToken, sessionID string) {
	header.Set("Authorization", "Bearer "+prefixAccessToken(accessToken))
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", clientUserAgent)
	header.Set("HTTP-Referer", "https://cline.bot")
	header.Set("X-Title", "Cline")
	header.Set("X-CLIENT-TYPE", "cline-sdk")
	header.Set("X-CLIENT-VERSION", clientVersion)
	header.Set("X-PLATFORM", "terminal")
	header.Set("X-Task-ID", sessionID)
}
