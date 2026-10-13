package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempStore(t *testing.T) (*AccountStore, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "opencode-accounts.json")
	store, err := LoadAccountStore(path)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}

	return store, path
}

func TestStoreAddDedupe(t *testing.T) {
	store, _ := tempStore(t)

	first := Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}
	if err := store.Add(first); err != nil {
		t.Fatalf("Add: %v", err)
	}

	second := first
	second.RefreshToken = "ref-2"
	second.OrgID, second.OrgName = "org-1", "Acme"
	if err := store.Add(second); err != nil {
		t.Fatalf("Add duplicate email: %v", err)
	}

	tokens := store.RefreshTokens()
	if len(tokens) != 1 || tokens[0] != "ref-2" {
		t.Fatalf("RefreshTokens = %v, want [ref-2]", tokens)
	}

	loaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := loaded.OrgID("ref-2"); got != "org-1" {
		t.Fatalf("OrgID = %q, want org-1", got)
	}
	if got := loaded.Server("ref-2"); got != defaultConsoleHost {
		t.Fatalf("Server = %q, want console host", got)
	}
}

func TestStoreRotate(t *testing.T) {
	store, _ := tempStore(t)

	if err := store.Add(Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Rotate("ref-1", "ref-2"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	tokens := store.RefreshTokens()
	if len(tokens) != 1 || tokens[0] != "ref-2" {
		t.Fatalf("RefreshTokens = %v, want [ref-2]", tokens)
	}

	loaded, err := LoadAccountStore(store.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if tokens := loaded.RefreshTokens(); len(tokens) != 1 || tokens[0] != "ref-2" {
		t.Fatalf("persisted RefreshTokens = %v, want [ref-2]", tokens)
	}
}

func TestStoreUnknownLookups(t *testing.T) {
	store, _ := tempStore(t)

	if got := store.Server("ref-missing"); got != "" {
		t.Fatalf("Server = %q, want empty", got)
	}
	if got := store.OrgID("ref-missing"); got != "" {
		t.Fatalf("OrgID = %q, want empty", got)
	}
}

func TestStoreNeverPersistsAccessToken(t *testing.T) {
	store, path := tempStore(t)

	if err := store.Add(Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, needle := range []string{"accessToken", "access_token", "acc-"} {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("accounts file contains %q", needle)
		}
	}
}
