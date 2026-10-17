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
	dirty    bool
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

	return s.noteWrite(accounts.Add(accountLabel, s.path, &s.accounts, account, func(a Account) string { return a.Email }))
}

func (s *AccountStore) Rotate(oldToken, newToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.noteWrite(accounts.Rotate(accountLabel, s.path, &s.accounts, oldToken, newToken, refreshToken, setRefreshToken))
}

// Flush retries the account-file write when a previous attempt failed, so a
// shutdown cannot lose a rotated refresh token — a spent token reuse is a ban
// trigger upstream — and force a re-login on the next start. It is a no-op
// when the file already reflects memory.
func (s *AccountStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	return s.noteWrite(accounts.Flush(accountLabel, s.path, &s.accounts, refreshToken))
}

// noteWrite remembers whether the in-memory entries are ahead of the file, so
// Flush knows whether there is anything to write.
func (s *AccountStore) noteWrite(err error) error {
	s.dirty = err != nil

	return err
}

func refreshToken(a Account) string { return a.RefreshToken }

func setRefreshToken(a *Account, token string) { a.RefreshToken = token }

func (s *AccountStore) persist() error {
	return accounts.Persist(accountLabel, s.path, s.accounts)
}
