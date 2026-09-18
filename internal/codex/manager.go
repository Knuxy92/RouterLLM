package codex

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
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
	m.mu.Lock()
	cached, ok := m.tokens[refreshToken]
	m.mu.Unlock()
	if !force && ok && cached.AccessToken != "" && time.Now().Add(tokenSkew).Before(cached.ExpiresAt) {
		return cached.AccessToken, nil
	}

	// The auth server rotates refresh tokens and rejects one that was already
	// spent (refresh_token_reused is a ban trigger upstream), so every refresh
	// after the first must send the newest token the previous rotation returned
	// rather than the caller's original key.
	current := refreshToken
	if ok && cached.RefreshToken != "" {
		current = cached.RefreshToken
	}

	token, err := m.client.Refresh(ctx, current)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.pruneExpiredLocked()
	m.tokens[refreshToken] = token
	m.mu.Unlock()

	// Persisting the rotated refresh token is best-effort: the account file may
	// sit on a read-only mount (Docker), and the in-memory token still serves.
	// Failing the request here would wrongly mark a healthy key dead.
	if m.store != nil {
		if err := m.store.Rotate(current, token.RefreshToken); err != nil {
			log.Printf("codex: rotated refresh token not persisted: %v", err)
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
	if m.store == nil {
		return Account{}, fmt.Errorf("no codex accounts file configured")
	}

	device, err := m.client.RequestDeviceAuth(ctx)
	if err != nil {
		return Account{}, err
	}
	if notify != nil {
		notify(device)
	}

	token, err := m.client.PollDeviceToken(ctx, device)
	if err != nil {
		return Account{}, err
	}
	if token.RefreshToken == "" {
		return Account{}, fmt.Errorf("codex device login returned an empty refresh token")
	}

	account := Account{
		AccountID:    "acc_" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		Email:        inspectAccessToken(token.AccessToken, 0).Email,
		RefreshToken: token.RefreshToken,
		CreatedAt:    time.Now(),
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
