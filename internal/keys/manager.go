package keys

import (
	"fmt"
	"sync"
	"time"
)

type entry struct {
	value     string
	deadUntil time.Time
	manual    bool
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

	for i := range n {
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
		if m.entries[i].value == value && !m.entries[i].manual {
			m.entries[i].deadUntil = deadUntil
		}
	}
}

// Manual disables survive only for the process lifetime (keys come from env
// placeholders and cannot be persisted to yaml). They are far-future deadUntil
// values under the hood, so hot-reload carries them across registry rebuilds
// via Snapshot/Restore like ordinary cooldowns — but a restart brings the key
// back.
func (m *Manager) SetDisabledByIndex(index int, disabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.entries) {
		return fmt.Errorf("key index %d out of range", index)
	}

	if disabled {
		m.entries[index].deadUntil = time.Now().AddDate(100, 0, 0)
		m.entries[index].manual = true
	} else {
		m.entries[index].deadUntil = time.Time{}
		m.entries[index].manual = false
	}

	return nil
}

func (m *Manager) LiveKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()

	for _, e := range m.entries {
		if !e.deadUntil.After(now) {
			return e.value
		}
	}

	if len(m.entries) > 0 {
		return m.entries[0].value
	}

	return ""
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

type State struct {
	Masked    string
	Alive     bool
	DeadUntil time.Time
	Manual    bool
}

func (m *Manager) States() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := make([]State, 0, len(m.entries))

	for _, e := range m.entries {
		out = append(out, State{
			Masked:    Mask(e.value),
			Alive:     !e.deadUntil.After(now),
			DeadUntil: e.deadUntil,
			Manual:    e.manual,
		})
	}

	return out
}

func (m *Manager) Snapshot() map[string]time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]time.Time, len(m.entries))

	for _, e := range m.entries {
		if !e.deadUntil.IsZero() {
			out[e.value] = e.deadUntil
		}
	}

	return out
}

func (m *Manager) Restore(state map[string]time.Time) {
	if len(state) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	manualHorizon := now.AddDate(50, 0, 0)

	for i := range m.entries {
		if deadUntil, ok := state[m.entries[i].value]; ok && deadUntil.After(now) {
			m.entries[i].deadUntil = deadUntil
			m.entries[i].manual = deadUntil.After(manualHorizon)
		}
	}
}
