package opencode

import (
	"sync"
	"time"

	"routerllm/internal/accounts"
)

const (
	accountsFileEnv     = "OPENCODE_ACCOUNTS_FILE"
	defaultAccountsFile = "opencode-accounts.json"
	accountLabel        = "opencode"
)

type Account struct {
	AccountID    string    `json:"accountId"`
	Email        string    `json:"email"`
	RefreshToken string    `json:"refreshToken"`
	Server       string    `json:"server"`
	OrgID        string    `json:"orgId,omitempty"`
	OrgName      string    `json:"orgName,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AccountStore struct {
	path     string
	mu       sync.Mutex
	accounts []Account
}

func DefaultAccountsPath() string {
	return accounts.DefaultPath(accountsFileEnv, defaultAccountsFile)
}

func LoadAccountStore(path string) (*AccountStore, error) {
	loaded, err := accounts.Load[Account](accountLabel, path)
	if err != nil {
		return nil, err
	}

	return &AccountStore{path: path, accounts: loaded}, nil
}

func (s *AccountStore) Path() string {
	return s.path
}

// RefreshTokens returns the non-empty refresh token of every entry, in order.
// The proxy uses these as the key pool; each is exchanged for a short-lived
// access token at request time.
func (s *AccountStore) RefreshTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Keys(s.accounts, func(a Account) string { return a.RefreshToken })
}

// Server returns the Console server of the account holding refreshToken, or
// "" when no account matches. Callers fall back to DefaultServer.
func (s *AccountStore) Server(refreshToken string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range s.accounts {
		if a.RefreshToken == refreshToken {
			return a.Server
		}
	}

	return ""
}

// OrgID returns the org id of the account holding refreshToken, or "" when
// the account has none. An empty org means no x-org-id header is sent.
func (s *AccountStore) OrgID(refreshToken string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range s.accounts {
		if a.RefreshToken == refreshToken {
			return a.OrgID
		}
	}

	return ""
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Add(accountLabel, s.path, &s.accounts, account, func(a Account) string { return a.Email })
}

func (s *AccountStore) Rotate(oldToken, newToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Rotate(accountLabel, s.path, &s.accounts, oldToken, newToken, refreshToken, setRefreshToken)
}

func refreshToken(a Account) string { return a.RefreshToken }

func setRefreshToken(a *Account, token string) { a.RefreshToken = token }
