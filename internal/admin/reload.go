package admin

import (
	"sync"
	"time"
)

type ReloadStatus struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

type ReloadTracker struct {
	mu   sync.Mutex
	last ReloadStatus
}

func NewReloadTracker() *ReloadTracker {
	return &ReloadTracker{}
}

func (t *ReloadTracker) RecordSuccess() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.last = ReloadStatus{At: time.Now(), OK: true}
}

func (t *ReloadTracker) RecordFailure(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.last = ReloadStatus{At: time.Now(), OK: false, Error: err.Error()}
}

func (t *ReloadTracker) Last() ReloadStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}
