package codex

import (
	"sync"
	"time"

	"routerllm/internal/accounts"
)

const (
	accountsFileEnv     = "CODEX_ACCOUNTS_FILE"
	defaultAccountsFile = "codex-accounts.json"
	accountLabel        = "codex"
)

type Account struct {
	AccountID    string    `json:"accountId"`
	Email        string    `json:"email"`
	RefreshToken string    `json:"refreshToken"`
	CreatedAt    time.Time `json:"createdAt"`
}

type accountsFile struct {
	Accounts []Account `json:"accounts"`
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

func (s *AccountStore) RefreshTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Keys(s.accounts, func(a Account) string { return a.RefreshToken })
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

func (s *AccountStore) persist() error {
	return accounts.Persist(accountLabel, s.path, s.accounts)
}
