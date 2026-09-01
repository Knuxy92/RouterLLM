package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// An anonymous client can spam /auth/challenge — the map must stay bounded.
func TestChallengeStoreCapsUnderFlood(t *testing.T) {
	store := NewSessionStore(func() string { return "secret" })
	for i := 0; i < maxChallenges+50; i++ {
		store.Challenge()
	}

	store.mu.Lock()
	count := len(store.challenges)
	store.mu.Unlock()

	if count != maxChallenges {
		t.Fatalf("challenges = %d, want cap %d enforced", count, maxChallenges)
	}
}

func TestChallengeIsSingleUseAndExpires(t *testing.T) {
	store := NewSessionStore(func() string { return "secret" })
	proof := func(nonce string) string {
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write([]byte(nonce))

		return hex.EncodeToString(mac.Sum(nil))
	}

	id, nonce, expiresIn := store.Challenge()
	if expiresIn != int(challengeTTL/time.Second) {
		t.Fatalf("expires_in = %d, want %d", expiresIn, int(challengeTTL/time.Second))
	}

	if _, _, ok := store.Verify(id, "not-hex"); ok {
		t.Fatal("verify accepted non-hex proof")
	}
	if _, _, ok := store.Verify(id, hex.EncodeToString(make([]byte, sha256.Size))); ok {
		t.Fatal("verify accepted zero proof")
	}
	// A correct proof after failed attempts must be rejected — the challenge
	// burned on the first attempt, win or lose.
	if _, _, ok := store.Verify(id, proof(nonce)); ok {
		t.Fatal("verify allowed reuse of a burned challenge")
	}

	// Expired challenges are dead even if never attempted.
	id, nonce, _ = store.Challenge()
	store.mu.Lock()
	ch := store.challenges[id]
	ch.expiry = time.Now().Add(-time.Second)
	store.challenges[id] = ch
	store.mu.Unlock()
	if _, _, ok := store.Verify(id, proof(nonce)); ok {
		t.Fatal("verify accepted an expired challenge")
	}
}

func TestSessionRequiresNonEmptyID(t *testing.T) {
	store := NewSessionStore(func() string { return "secret" })

	if store.Valid("") {
		t.Fatal("empty session id considered valid")
	}
	if store.Valid("forged") {
		t.Fatal("unknown session id considered valid")
	}
}
