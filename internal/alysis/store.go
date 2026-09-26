package alysis

import (
	"sync"
	"time"

	"routerllm/internal/accounts"
)

const (
	accountsFileEnv = "ALYSIS_ACCOUNTS_FILE"
	accountLabel    = "alysis"
)

type Account struct {
	AccountID  string    `json:"accountId"`
	Email      string    `json:"email,omitempty"`
	GatewayKey string    `json:"gatewayKey"`
	CreatedAt  time.Time `json:"createdAt"`
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
	return accounts.DefaultPath(accountsFileEnv, "alysis-accounts.json")
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

func (s *AccountStore) GatewayKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Keys(s.accounts, func(a Account) string { return a.GatewayKey })
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Add(accountLabel, s.path, &s.accounts, account, func(a Account) string { return a.GatewayKey })
}

func (s *AccountStore) persist() error {
	return accounts.Persist(accountLabel, s.path, s.accounts)
}
