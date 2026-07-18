package keys

import (
	"sync"
	"time"
)

type entry struct {
	value     string
	deadUntil time.Time
}

type Manager struct {
	mu       sync.Mutex
	entries  []entry
	cursor   int
	cooldown time.Duration
}

func New(values []string, cooldown time.Duration) *Manager {
	entries := make([]entry, 0, len(values))
	for _, v := range values {
		entries = append(entries, entry{value: v})
	}
	return &Manager{entries: entries, cooldown: cooldown}
}

func (m *Manager) Next() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return "", false
	}
	now := time.Now()
	n := len(m.entries)
	for i := 0; i < n; i++ {
		idx := (m.cursor + i) % n
		if m.entries[idx].deadUntil.After(now) {
			continue
		}
		m.cursor = (idx + 1) % n
		return m.entries[idx].value, true
	}
	return "", false
}

func (m *Manager) MarkDead(value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deadUntil := time.Now().Add(m.cooldown)
	for i := range m.entries {
		if m.entries[i].value == value {
			m.entries[i].deadUntil = deadUntil
		}
	}
}

func (m *Manager) AliveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	alive := 0
	for _, e := range m.entries {
		if !e.deadUntil.After(now) {
			alive++
		}
	}
	return alive
}

func Mask(value string) string {
	if len(value) <= 4 {
		return "..."
	}
	return "..." + value[len(value)-4:]
}
