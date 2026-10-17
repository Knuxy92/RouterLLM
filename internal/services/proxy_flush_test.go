package services

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"routerllm/internal/opencode"
	"routerllm/internal/provider"
)

func TestFlushAccountsRetriesFailedAccountWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode-accounts.json")
	store, err := opencode.LoadAccountStore(path)
	if err != nil {
		t.Fatalf("LoadAccountStore: %v", err)
	}
	if err := store.Add(opencode.Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Make the rotation's write fail the way a locked or read-only account
	// file does: the directory disappears between the load and the persist.
	dir := filepath.Dir(path)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := store.Rotate("ref-1", "ref-2"); err == nil {
		t.Fatal("Rotate wrote through a missing directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("recreate dir: %v", err)
	}

	proxy := NewProxy(provider.NewRegistry(nil, nil, time.Minute), nil, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	proxy.opencodeManagers["console"] = opencode.NewManager(&opencode.Client{}, store)

	proxy.FlushAccounts()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var file struct {
		Accounts []struct {
			RefreshToken string `json:"refreshToken"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(file.Accounts) != 1 || file.Accounts[0].RefreshToken != "ref-2" {
		t.Fatalf("accounts = %s, want the rotated token persisted", raw)
	}
}
