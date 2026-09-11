package alysis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const accountsFileEnv = "ALYSIS_ACCOUNTS_FILE"

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
	if path := os.Getenv(accountsFileEnv); path != "" {
		return path
	}

	exe, err := os.Executable()
	if err != nil {
		return "alysis-accounts.json"
	}

	return filepath.Join(filepath.Dir(exe), "alysis-accounts.json")
}

func LoadAccountStore(path string) (*AccountStore, error) {
	store := &AccountStore{path: path}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read alysis accounts %q: %w", path, err)
	}

	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse alysis accounts %q: %w", path, err)
	}
	store.accounts = file.Accounts

	return store, nil
}

func (s *AccountStore) Path() string {
	return s.path
}

func (s *AccountStore) GatewayKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.GatewayKey != "" {
			keys = append(keys, account.GatewayKey)
		}
	}

	return keys
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.accounts {
		if existing.GatewayKey != "" && existing.GatewayKey == account.GatewayKey {
			s.accounts[i] = account

			return s.persist()
		}
	}
	s.accounts = append(s.accounts, account)

	return s.persist()
}

func (s *AccountStore) persist() error {
	raw, err := json.MarshalIndent(accountsFile{Accounts: s.accounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode alysis accounts: %w", err)
	}

	return writeFileAtomic(s.path, raw, 0600)
}

// writeFileAtomic writes through a temp file in the same directory and renames
// it over the target, so a crash mid-write cannot leave a truncated account
// file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write alysis accounts %q: %w", path, err)
	}

	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write alysis accounts %q: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write alysis accounts %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write alysis accounts %q: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write alysis accounts %q: %w", path, err)
	}

	return nil
}
