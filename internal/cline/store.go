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

// Rotate rewrites the account entry holding oldToken. The file is re-read
// first so an account added by another process (a concurrent --cline-login)
// survives the rewrite instead of being dropped from disk.
func (s *AccountStore) Rotate(oldToken, newToken string) error {
	if oldToken == newToken || newToken == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.mergeFileLocked(); err != nil {
		return err
	}

	rotated := false
	for i, account := range s.accounts {
		if account.RefreshToken == oldToken {
			s.accounts[i].RefreshToken = newToken
			rotated = true
		}
	}
	if !rotated {
		return nil
	}

	return s.persist()
}

// mergeFileLocked folds accounts that appeared in the file since it was loaded
// into memory. A missing file is not an error: the caller may deliberately
// point the store at a path that does not exist yet. Caller must hold s.mu.
func (s *AccountStore) mergeFileLocked() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read cline accounts %q: %w", s.path, err)
	}

	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse cline accounts %q: %w", s.path, err)
	}

	known := make(map[string]bool, len(s.accounts))
	for _, account := range s.accounts {
		known[account.RefreshToken] = true
	}
	for _, account := range file.Accounts {
		if account.RefreshToken == "" || known[account.RefreshToken] {
			continue
		}
		s.accounts = append(s.accounts, account)
		known[account.RefreshToken] = true
	}

	return nil
}

func (s *AccountStore) persist() error {
	raw, err := json.MarshalIndent(accountsFile{Accounts: s.accounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cline accounts: %w", err)
	}

	return writeFileAtomic(s.path, raw, 0600)
}

// writeFileAtomic writes through a temp file in the same directory and renames
// it over the target, so a crash mid-write cannot leave a truncated account
// file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write cline accounts %q: %w", path, err)
	}

	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write cline accounts %q: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write cline accounts %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write cline accounts %q: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write cline accounts %q: %w", path, err)
	}

	return nil
}
