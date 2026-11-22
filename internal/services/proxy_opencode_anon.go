package services

import (
	"sync"
	"time"
)

// opencodeAnonKey is the credential the anonymous attempt carries: the
// placeholder token the official CLI sends when signed out (its provider
// plugin sets apiKey "public"). It doubles as the pseudo-key so the
// anonymous attempt flows through the key-facing helpers — logs, telemetry,
// pool guards — with an account store that has no entries.
const opencodeAnonKey = "public"

// anonCooldown is how long bolt mode stays on the login leg after the
// gateway rejects anonymous traffic. It matches the config default
// cooldown (defaultCooldown in internal/config); the registry does not
// carry that value into the proxy.
const anonCooldown = 60 * time.Second

// anonGate keeps bolt mode from hammering the anonymous door: one trip
// shuts anonymous out for the cooldown window while the login pool serves,
// and the gate reopens by itself once the window passes. The two methods
// are the whole interface.
type anonGate struct {
	mu        sync.Mutex
	deadUntil time.Time
}

// allowed reports whether an anonymous attempt may run at now.
func (g *anonGate) allowed(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return !now.Before(g.deadUntil)
}

// trip closes the gate until now+cooldown.
func (g *anonGate) trip(now time.Time, cooldown time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.deadUntil = now.Add(cooldown)
}

// anonGateFor returns the proxy-lifetime gate for one provider name.
func (p *Proxy) anonGateFor(name string) *anonGate {
	v, _ := p.anonGates.LoadOrStore(name, &anonGate{})

	return v.(*anonGate)
}