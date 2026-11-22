package kilo

import (
	"sync"
	"time"

	"routerllm/internal/accounts"
)

const (
	accountsFileEnv = "KILO_ACCOUNTS_FILE"
	accountLabel    = "kilo"
)

type Account struct {
	AccountID      string    `json:"accountId"`
	Email          string    `json:"email,omitempty"`
	OrganizationID string    `json:"organizationId,omitempty"`
	Token          string    `json:"token"`
	CreatedAt      time.Time `json:"createdAt"`
}

type AccountStore struct {
	path     string
	mu       sync.Mutex
	accounts []Account
}

func DefaultAccountsPath() string {
	return accounts.DefaultPath(accountsFileEnv, "kilo-accounts.json")
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

	return accounts.Keys(s.accounts, func(a Account) string { return a.Token })
}

// OrganizationID returns the account's organization id and whether the token
// exists in the store. An account without an organization reports ("", true) —
// omitting the header for it is correct; a token that is not in the store
// reports ("", false).
func (s *AccountStore) OrganizationID(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, account := range s.accounts {
		if account.Token == token {
			return account.OrganizationID, true
		}
	}

	return "", false
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Add(accountLabel, s.path, &s.accounts, account, func(a Account) string { return a.Token })
}
