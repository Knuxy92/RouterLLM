package workbuddy

import (
	"context"
	"log"
	"sync"
	"time"
)

const tokenSkew = time.Minute

type Manager struct {
	client   *Client
	store    *AccountStore
	mu       sync.Mutex
	tokens   map[string]Token
	inflight map[string]*refreshCall
}

// refreshCall lets concurrent callers holding the same refresh token share one
// upstream exchange: the first becomes the leader, the rest wait on done.
type refreshCall struct {
	done  chan struct{}
	token Token
	err   error
}

func NewManager(client *Client, store *AccountStore) *Manager {
	if client == nil {
		client = &Client{}
	}

	return &Manager{
		client:   client,
		store:    store,
		tokens:   make(map[string]Token),
		inflight: make(map[string]*refreshCall),
	}
}

// AccessToken returns a live access token for refreshToken, refreshing when the
// cached one is missing or expiring. The gateway rotates refresh tokens, so
// every refresh after the first sends the newest token the previous rotation
// returned rather than the caller's original key.
func (m *Manager) AccessToken(ctx context.Context, realm, uid, refreshToken string, force bool) (string, error) {
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

	call, leader := m.beginRefresh(refreshToken)
	if !leader {
		select {
		case <-call.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}

		if call.err != nil {
			return "", call.err
		}

		return call.token.AccessToken, nil
	}

	token, err := m.client.Refresh(ctx, realm, uid, current)
	m.finishRefresh(refreshToken, call, token, err)
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
			log.Printf("workbuddy: rotated refresh token not persisted: %v", err)
		}
	}

	return token.AccessToken, nil
}

// beginRefresh registers a refresh for refreshToken. The first caller leads and
// performs the exchange; later callers wait for its result.
func (m *Manager) beginRefresh(refreshToken string) (*refreshCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if call, ok := m.inflight[refreshToken]; ok {
		return call, false
	}

	call := &refreshCall{done: make(chan struct{})}
	m.inflight[refreshToken] = call

	return call, true
}

// finishRefresh publishes the leader's outcome and wakes the waiters.
func (m *Manager) finishRefresh(refreshToken string, call *refreshCall, token Token, err error) {
	m.mu.Lock()
	call.token = token
	call.err = err
	delete(m.inflight, refreshToken)
	m.mu.Unlock()

	close(call.done)
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
