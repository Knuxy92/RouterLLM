package cline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const accountsFileEnv = "CLINE_ACCOUNTS_FILE"

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
	if path := os.Getenv(accountsFileEnv); path != "" {
		return path
	}

	exe, err := os.Executable()
	if err != nil {
		return "cline-accounts.json"
	}

	return filepath.Join(filepath.Dir(exe), "cline-accounts.json")
}

func LoadAccountStore(path string) (*AccountStore, error) {
	store := &AccountStore{path: path}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cline accounts %q: %w", path, err)
	}

	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse cline accounts %q: %w", path, err)
	}
	store.accounts = file.Accounts

	return store, nil
}

func (s *AccountStore) Path() string {
	return s.path
}

func (s *AccountStore) RefreshTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := make([]string, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.RefreshToken != "" {
			tokens = append(tokens, account.RefreshToken)
		}
	}

	return tokens
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.accounts {
		if existing.Email != "" && existing.Email == account.Email {
			s.accounts[i] = account
			return s.persist()
		}
	}
	s.accounts = append(s.accounts, account)

	return s.persist()
}

func (s *AccountStore) Rotate(oldToken, newToken string) error {
	if oldToken == newToken || newToken == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, account := range s.accounts {
		if account.RefreshToken == oldToken {
			s.accounts[i].RefreshToken = newToken
			return s.persist()
		}
	}

	return nil
}

func (s *AccountStore) persist() error {
	raw, err := json.MarshalIndent(accountsFile{Accounts: s.accounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cline accounts: %w", err)
	}

	if err := os.WriteFile(s.path, raw, 0600); err != nil {
		return fmt.Errorf("write cline accounts %q: %w", s.path, err)
	}

	return nil
}
