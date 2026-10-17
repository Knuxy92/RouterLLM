package opencode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlushPersistsFailedRotation(t *testing.T) {
	store, path := tempStore(t)

	if err := store.Add(Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}); err != nil {
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

	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	loaded, err := LoadAccountStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if tokens := loaded.RefreshTokens(); len(tokens) != 1 || tokens[0] != "ref-2" {
		t.Fatalf("persisted RefreshTokens = %v, want [ref-2] (flush lost the rotation)", tokens)
	}
}

func TestFlushIsNoOpWhenClean(t *testing.T) {
	store, path := tempStore(t)

	if err := store.Add(Account{AccountID: "u-1", Email: "dev@example.com", RefreshToken: "ref-1", Server: defaultConsoleHost, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// A clean store must not touch the disk: with the directory gone, any
	// write attempt would fail loudly instead of silently succeeding.
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("clean Flush wrote to disk: %v", err)
	}
}
