package cline

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	clientVersion   = "3.0.60"
	coreVersion     = "0.0.81"
	clientUserAgent = "Cline/" + clientVersion + " ai-sdk/openai-compatible/3.0.30 ai-sdk/provider-utils/5.0.27 runtime/bun/1.3.13"

	// taskIDAlphabet is the CLI's lowercase-alphanumeric suffix alphabet.
	taskIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
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

	// Persisting the rotated refresh token is best-effort: the account file may
	// sit on a read-only mount (Docker), and the in-memory token still serves.
	// Failing the request here would wrongly mark a healthy key dead.
	if m.store != nil {
		if err := m.store.Rotate(refreshToken, token.RefreshToken); err != nil {
			log.Printf("cline: rotated refresh token not persisted: %v", err)
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

// newTaskID mimics the CLI's X-Task-ID: <unix-millis>_<5 lowercase alphanumerics>.
func newTaskID() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixMilli(), 10) + "_aaaaa"
	}
	for i := range b {
		b[i] = taskIDAlphabet[int(b[i])%len(taskIDAlphabet)]
	}

	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + string(b)
}

// PrepareBody returns the task id for the X-Task-ID header. The official CLI
// sends no session_id field in the body, so client-supplied values are left
// untouched and none is injected.
func PrepareBody(body map[string]any) string {
	if sessionID, _ := body["session_id"].(string); sessionID != "" {
		return sessionID
	}

	return newTaskID()
}

func SetHeaders(header http.Header, accessToken, sessionID string) {
	header.Set("Authorization", "Bearer "+prefixAccessToken(accessToken))
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", clientUserAgent)
	header.Set("HTTP-Referer", "https://cline.bot")
	header.Set("X-Title", "Cline")
	header.Set("X-CLIENT-TYPE", "cline-cli")
	header.Set("X-CLIENT-VERSION", clientVersion)
	header.Set("X-PLATFORM", "cli")
	header.Set("X-PLATFORM-VERSION", clientVersion)
	header.Set("X-CORE-VERSION", coreVersion)
	header.Set("X-IS-MULTIROOT", "false")
	header.Set("X-Task-ID", sessionID)
}
