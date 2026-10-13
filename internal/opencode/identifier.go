package opencode

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

const (
	// Shape of an OpenCode identifier, mirroring
	// packages/schema/src/identifier.ts in the client: 12 hex characters that
	// encode a timestamp plus a per-millisecond counter, followed by 14 random
	// base62 characters.
	//
	// The gateway decodes that prefix to decide whether the caller is a real
	// OpenCode client, so the shape is load-bearing, not cosmetic. See
	// newIdentifier.
	idLength   = 26
	idHexBytes = 6
	idRandom   = idLength - idHexBytes*2

	// Digits, then upper case, then lower case. The gateway does not care
	// which; keeping the client's order keeps our ids comparable with its own.
	idCharset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	hexDigits = "0123456789abcdef"

	// The timestamp is shifted left by this many bits so the low bits can hold
	// the counter.
	idCounterShift = 12
)

var (
	// Counter shared across calls so ids minted in the same millisecond sort in
	// creation order, which is what the client's module does. The gateway
	// rejects a counter past a few dozen, so this must not drift.
	idMu      sync.Mutex
	idLastMS  int64
	idCounter uint16
)

// newIdentifier returns prefix followed by an OpenCode-shaped identifier.
//
// The shape is what the free tier authenticates. The 12 hex characters are
// ~(timestamp<<12 | counter); the 14 that follow are random base62. An id of
// plain hex, or one whose prefix decodes far from now, is refused with a 403
// FreeTierError by the gateway — so this must not be simplified to random hex
// even though it would look equivalent.
func newIdentifier(prefix string) string {
	idMu.Lock()
	now := time.Now().UnixMilli()
	if now != idLastMS {
		idLastMS = now
		idCounter = 0
	}
	idCounter++
	counter := idCounter
	idMu.Unlock()

	// Descending, so the newest id sorts first.
	value := (uint64(now) << idCounterShift) | uint64(counter)
	value = ^value

	random := make([]byte, idRandom)
	if _, err := rand.Read(random); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}

	var b strings.Builder
	b.Grow(len(prefix) + idLength)
	b.WriteString(prefix)
	for i := 0; i < idHexBytes; i++ {
		octet := byte(value >> (8 * (idHexBytes - 1 - i)))
		b.WriteByte(hexDigits[octet>>4])
		b.WriteByte(hexDigits[octet&0x0f])
	}
	for _, v := range random {
		b.WriteByte(idCharset[int(v)%len(idCharset)])
	}

	return b.String()
}
