package workbuddy

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"routerllm/internal/accounts"
)

const (
	accountsFileEnv     = "WORKBUDDY_ACCOUNTS_FILE"
	defaultAccountsFile = "workbuddy-accounts.json"
	accountLabel        = "workbuddy"
)

type Account struct {
	UID          string    `json:"uid"`
	Email        string    `json:"email"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	Realm        string    `json:"realm"`
	Product      string    `json:"product"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    int64     `json:"expiresAt"`
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

// RefreshTokens returns the non-empty refresh token of every entry, in order.
// The proxy uses these as the key pool; each is exchanged for a short-lived
// access token at request time.
func (s *AccountStore) RefreshTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return accounts.Keys(s.accounts, refreshToken)
}

// Realm returns the realm of the account holding refreshToken, or "" when no
// account matches.
func (s *AccountStore) Realm(refreshToken string) string {
	account, ok := s.lookup(refreshToken)
	if !ok {
		return ""
	}

	return account.Realm
}

// Product returns the product of the account holding refreshToken, or "" when
// no account matches.
func (s *AccountStore) Product(refreshToken string) string {
	account, ok := s.lookup(refreshToken)
	if !ok {
		return ""
	}

	return account.Product
}

// UID returns the account id of the account holding refreshToken, falling back
// to the uid or sub claim of its access token, or "" when neither is present.
func (s *AccountStore) UID(refreshToken string) string {
	account, ok := s.lookup(refreshToken)
	if !ok {
		return ""
	}
	if account.UID != "" {
		return account.UID
	}

	return jwtUID(account.AccessToken)
}

// lookup returns the account whose refresh token matches, if any.
func (s *AccountStore) lookup(refreshToken string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, account := range s.accounts {
		if account.RefreshToken == refreshToken {
			return account, true
		}
	}

	return Account{}, false
}

// AccountUID decodes the payload segment of a JWT access token and returns
// its "uid" or "sub" claim, or "" when the token is not a readable JWT. The
// login command uses it to fill the account's UID from the token the poll
// returned.
func AccountUID(accessToken string) string {
	return jwtUID(accessToken)
}

// jwtUID decodes the payload segment of a JWT access token and returns its
// "uid" or "sub" claim, or "" when the token is not a readable JWT.
func jwtUID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		UID string `json:"uid"`
		Sub string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	if claims.UID != "" {
		return claims.UID
	}

	return claims.Sub
}

func (s *AccountStore) Add(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.noteWrite(accounts.Add(accountLabel, s.path, &s.accounts, account, refreshToken))
}

func (s *AccountStore) Rotate(oldToken, newToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.noteWrite(accounts.Rotate(accountLabel, s.path, &s.accounts, oldToken, newToken, refreshToken, setRefreshToken))
}

// Flush retries the account-file write when a previous attempt failed, so a
// shutdown cannot lose a rotated refresh token and force a re-login on the
// next start. It is a no-op when the file already reflects memory.
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

func refreshToken(account Account) string { return account.RefreshToken }

func setRefreshToken(account *Account, token string) { account.RefreshToken = token }
