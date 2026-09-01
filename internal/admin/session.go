package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

const (
	challengeTTL = 60 * time.Second
	sessionTTL   = 12 * time.Hour
	maxSessions  = 100
	// Challenge is the only unauthenticated endpoint, so its map is the one
	// an anonymous client can grow; cap it like sessions.
	maxChallenges = 1000
)

type challenge struct {
	nonce  string
	expiry time.Time
}

// SessionStore issues single-use challenges and validates HMAC proofs. The
// admin secret itself never crosses the wire: the client proves knowledge of
// it by computing HMAC-SHA256(secret, nonce). State is bounded — sessions are
// capped at maxSessions with lazy expiry sweeps, matching the repo policy of
// no unbounded in-memory growth.
type SessionStore struct {
	secret func() string

	mu         sync.Mutex
	challenges map[string]challenge
	sessions   map[string]time.Time
}

func NewSessionStore(secret func() string) *SessionStore {
	return &SessionStore{
		secret:     secret,
		challenges: make(map[string]challenge),
		sessions:   make(map[string]time.Time),
	}
}

func randomToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}

	return hex.EncodeToString(raw)
}

// Challenge returns {id, nonce} for one proof attempt. The nonce expires after
// challengeTTL and is consumed on the first verify, win or lose.
func (s *SessionStore) Challenge() (id, nonce string, expiresIn int) {
	id = randomToken()
	nonce = randomToken()

	s.mu.Lock()
	s.sweepLocked()
	if len(s.challenges) >= maxChallenges {
		s.dropOldestChallengeLocked()
	}
	s.challenges[id] = challenge{nonce: nonce, expiry: time.Now().Add(challengeTTL)}
	s.mu.Unlock()

	return id, nonce, int(challengeTTL / time.Second)
}

// Verify consumes the challenge and, if proof == hex(HMAC-SHA256(secret, nonce)),
// mints a session id. A wrong proof burns the challenge — no retries on the
// same nonce.
func (s *SessionStore) Verify(id, proof string) (session string, expiresIn int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	ch, live := s.challenges[id]
	if !live {
		return "", 0, false
	}
	delete(s.challenges, id)
	if time.Now().After(ch.expiry) {
		return "", 0, false
	}

	mac := hmac.New(sha256.New, []byte(s.secret()))
	mac.Write([]byte(ch.nonce))
	expected := mac.Sum(nil)

	given, err := hex.DecodeString(proof)
	if err != nil || len(given) != sha256.Size {
		return "", 0, false
	}
	if subtle.ConstantTimeCompare(given, expected) != 1 {
		return "", 0, false
	}

	session = randomToken()
	if len(s.sessions) >= maxSessions {
		s.dropOldestSessionLocked()
	}
	s.sessions[session] = time.Now().Add(sessionTTL)

	return session, int(sessionTTL / time.Second), true
}

// Valid reports whether a session id is live.
func (s *SessionStore) Valid(session string) bool {
	if session == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	expiry, ok := s.sessions[session]

	return ok && time.Now().Before(expiry)
}

// sweepLocked drops expired challenges and sessions. Called on every access so
// the maps never grow without bound even if challenges are issued and abandoned.
func (s *SessionStore) sweepLocked() {
	now := time.Now()
	for id, ch := range s.challenges {
		if now.After(ch.expiry) {
			delete(s.challenges, id)
		}
	}
	for id, expiry := range s.sessions {
		if now.After(expiry) {
			delete(s.sessions, id)
		}
	}
}

func (s *SessionStore) dropOldestSessionLocked() {
	oldestID := ""
	oldest := time.Time{}
	for id, expiry := range s.sessions {
		if oldestID == "" || expiry.Before(oldest) {
			oldestID, oldest = id, expiry
		}
	}
	if oldestID != "" {
		delete(s.sessions, oldestID)
	}
}

func (s *SessionStore) dropOldestChallengeLocked() {
	oldestID := ""
	var oldest challenge
	for id, ch := range s.challenges {
		if oldestID == "" || ch.expiry.Before(oldest.expiry) {
			oldestID, oldest = id, ch
		}
	}
	if oldestID != "" {
		delete(s.challenges, oldestID)
	}
}
