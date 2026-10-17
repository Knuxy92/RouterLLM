package opencode

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

// AccessToken returns a live access token for refreshToken, refreshing when
// the cached one is missing or expiring. The Console rotates refresh tokens,
// so every refresh after the first sends the newest token the previous
// rotation returned rather than the caller's original key.
func (m *Manager) AccessToken(ctx context.Context, refreshToken string, force bool) (string, error) {
	m.mu.Lock()
	cached, ok := m.tokens[refreshToken]
	m.mu.Unlock()
	if !force && ok && cached.AccessToken != "" && time.Now().Add(tokenSkew).Before(cached.ExpiresAt) {
		return cached.AccessToken, nil
	}

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
			log.Printf("opencode: rotated refresh token not persisted: %v", err)
		}
	}

	return token.AccessToken, nil
}

// Flush retries persisting account-file updates this process made but could
// not write when they happened — a locked file, a read-only mount. Called on
// shutdown so the file is never left behind the rotations this process served.
func (m *Manager) Flush() error {
	if m.store == nil {
		return nil
	}

	return m.store.Flush()
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

// Login runs the Console device flow: the notify callback shows the browser
// URL and user code, then polling waits for the user to approve. The returned
// account carries everything the proxy needs at request time.
func (m *Manager) Login(ctx context.Context, notify func(DeviceAuth)) (Account, error) {
	if m.store == nil {
		return Account{}, fmt.Errorf("no opencode accounts file configured")
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
		return Account{}, fmt.Errorf("opencode device login returned an empty refresh token")
	}

	identity, err := m.client.FetchIdentity(ctx, token.AccessToken, token.OrgID)
	if err != nil {
		return Account{}, err
	}

	server := m.client.server()
	account := Account{
		AccountID:    "acc_" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		Email:        identity.Email,
		RefreshToken: token.RefreshToken,
		Server:       server,
		OrgID:        identity.OrgID,
		OrgName:      identity.OrgName,
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
