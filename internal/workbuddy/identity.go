package workbuddy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// CLI identity constants, verified against the official
// @tencent-ai/codebuddy-code 2.168.2 npm package: the CLI version (both UA
// segments), and the OpenAI Node SDK 6.25.0 fingerprint values it ships with.
const (
	cliIDEVersion           = "2.168.2"
	stainlessPackageVersion = "6.25.0"
	stainlessRuntimeVersion = "v22.21.1"
)

// MachineID derives the stable per-account X-Machine-ID: the first 32 hex
// chars of sha256("machine|<uid>|<salt>"). The salt is fixed so the same
// account always presents the same machine identity.
func MachineID(uid, salt string) string {
	return hashedID("machine", uid, salt)
}

// SessionID derives the stable per-account X-Session-ID, same recipe as
// MachineID with the "session" prefix.
func SessionID(uid, salt string) string {
	return hashedID("session", uid, salt)
}

func hashedID(kind, uid, salt string) string {
	sum := sha256.Sum256([]byte(kind + "|" + uid + "|" + salt))

	return hex.EncodeToString(sum[:])[:32]
}

// UUID7Hex returns a 32-char lowercase hex RFC 9562 UUIDv7: 48-bit unix
// millisecond timestamp, version 7, 12-bit rand_a, variant 10, 62-bit rand_b.
// The upstream conversation/request/message ids are this shape without dashes.
func UUID7Hex() string {
	ms := time.Now().UnixMilli() & ((1 << 48) - 1)

	var buf [8]byte
	_, _ = rand.Read(buf[:]) // crypto/rand is documented to never fail
	randA := uint64(binary.BigEndian.Uint16(buf[:2]) & 0x0FFF)
	randB := binary.BigEndian.Uint64(buf[:]) & ((1 << 62) - 1)

	hi := uint64(ms)<<16 | 0x7<<12 | randA
	lo := uint64(0b10)<<62 | randB

	return fmt.Sprintf("%016x%016x", hi, lo)
}

// ConversationID returns a dashed UUID for the X-Conversation-ID family.
// With a seed it is derived deterministically (sha256 of
// "conversation|<seed>" with v4 version/variant bits set), so one account's
// one session always presents the same conversation id instead of a fresh
// throwaway per request; without a seed it is a fresh UUIDv4.
func ConversationID(seed string) string {
	if seed == "" {
		return newUUID4()
	}

	sum := sha256.Sum256([]byte("conversation|" + seed))
	h := []byte(hex.EncodeToString(sum[:])[:32])
	h[12] = '4' // UUID version nibble
	h[16] = 'a' // RFC 4122 variant nibble

	return string(h[0:8]) + "-" + string(h[8:12]) + "-" + string(h[12:16]) + "-" + string(h[16:20]) + "-" + string(h[20:32])
}

func newUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// randSpan returns n random bytes as 2n lowercase hex chars (trace span ids).
func randSpan(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b)
}

// stainlessOS mirrors the OpenAI Node SDK's per-host OS value.
func stainlessOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "MacOS"
	default:
		return "Linux"
	}
}

// stainlessArch mirrors the SDK's per-host arch value.
func stainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

// Identity describes one outbound WorkBuddy client identity. The baseline is
// the desktop client; Product "cli" swaps the IDE headers to the official
// CodeBuddy CLI family. Realm is anything NormalizeRealm accepts.
type Identity struct {
	UID         string
	AccessToken string
	Realm       string
	Product     string
	MachineSalt string
	// SessionKey stabilises the CLI identity's conversation id per
	// (uid, session); the desktop identity generates a fresh one per request.
	SessionKey string
	// TurnID is the per-turn request id every attempt of one user send
	// reuses; empty means a fresh id is minted here.
	TurnID      string
	Model       string
	PrivateData bool
}

// Headers builds the outbound identity header family for one request, in the
// shape the desktop-client captures verified (wb_identity): X-Conversation-ID
// fresh per request, X-Conversation-Request-ID/X-Root-Request-ID per turn,
// X-Request-ID = X-Conversation-Message-ID per request, plus the B3 trace
// family. The CLI identity derives its conversation id from (uid, session),
// reuses TurnID, and adds the x-stainless-* SDK fingerprint block. Names are
// assigned verbatim — the official wire casing (X-IDE-*, lowercase
// b3/traceparent/x-stainless-*) must survive, so nothing goes through
// http.Header.Set's canonicalization.
func (id Identity) Headers() http.Header {
	realm := NormalizeRealm(id.Realm)

	cli := NormalizeProduct(id.Product) == "cli"
	ideType, ideName, ua := "WorkBuddy", "WorkBuddy", DesktopUA(realm)
	ideVersion := DesktopVersion(realm)
	if cli {
		ideType, ideName = "CLI", "CLI"
		ideVersion = cliIDEVersion
		ua = "CLI/" + cliIDEVersion + " CodeBuddy/" + cliIDEVersion
	}

	msgID := UUID7Hex()
	traceID := UUID7Hex()
	span16 := randSpan(8)

	turnID := id.TurnID
	if turnID == "" {
		turnID = UUID7Hex()
	}

	convID := newUUID4()
	if cli {
		convID = ConversationID(id.UID + "|" + id.SessionKey)
	}

	h := make(http.Header)
	setHeader(h, "User-Agent", ua)
	setHeader(h, "X-IDE-Type", ideType)
	setHeader(h, "X-IDE-Name", ideName)
	setHeader(h, "X-IDE-Version", ideVersion)
	setHeader(h, "X-Product", "SaaS")
	setHeader(h, "X-Agent-Intent", "craft")
	setHeader(h, "X-Agent-Purpose", "conversation")
	setHeader(h, "X-Agent-Type", "main")
	if cli {
		setHeader(h, "X-Agent-Runtime", "other")
		setHeader(h, "X-Private-Data", strconv.FormatBool(id.PrivateData))
	} else {
		setHeader(h, "X-Private-Data", "true")
	}
	setHeader(h, "Accept", "application/json, text/plain, */*")
	setHeader(h, "Authorization", "Bearer "+id.AccessToken)
	setHeader(h, "X-User-Id", id.UID)
	setHeader(h, "X-Domain", Hosts(realm).Domain)
	setHeader(h, "X-Conversation-ID", convID)
	setHeader(h, "X-Conversation-Request-ID", turnID)
	setHeader(h, "X-Root-Request-ID", turnID)
	setHeader(h, "X-Conversation-Message-ID", msgID)
	setHeader(h, "X-Request-ID", msgID)

	// Trace family: the desktop request is a root span (three-segment b3, no
	// parent); the official CLI is a child span (four-segment b3 + parent).
	setHeader(h, "X-Trace-ID", traceID)
	setHeader(h, "X-B3-TraceId", traceID)
	setHeader(h, "X-B3-SpanId", span16)
	setHeader(h, "X-B3-Sampled", "1")
	setHeader(h, "traceparent", "00-"+traceID+"-"+span16+"-01")
	if cli {
		parent := randSpan(8)
		setHeader(h, "X-B3-ParentSpanId", parent)
		setHeader(h, "b3", traceID+"-"+span16+"-1-"+parent)
	} else {
		setHeader(h, "b3", traceID+"-"+span16+"-1")
	}

	if cli {
		// The official CLI speaks through the OpenAI Node SDK, whose
		// x-stainless-* fingerprint headers ride the wire lowercased.
		setHeader(h, "x-stainless-lang", "js")
		setHeader(h, "x-stainless-package-version", stainlessPackageVersion)
		setHeader(h, "x-stainless-os", stainlessOS())
		setHeader(h, "x-stainless-arch", stainlessArch())
		setHeader(h, "x-stainless-runtime", "node")
		setHeader(h, "x-stainless-runtime-version", stainlessRuntimeVersion)
		setHeader(h, "x-stainless-retry-count", "0")
		setHeader(h, "x-stainless-timeout", "1200")
	}

	return h
}

// setHeader stores name=value verbatim, bypassing http.Header's
// canonicalization (Set would rewrite X-IDE-Type to X-Ide-Type).
func setHeader(h http.Header, name, value string) {
	h[name] = []string{value}
}
