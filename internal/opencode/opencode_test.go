package opencode

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// idSuffixValid checks the two-part shape the gateway decodes: 12 hex
// characters carrying the timestamp and counter, then 14 base62 characters.
func idSuffixValid(s string) bool {
	if len(s) != idLength {
		return false
	}
	hexPart, randomPart := s[:idHexBytes*2], s[idHexBytes*2:]
	for _, r := range hexPart {
		if !strings.ContainsRune(hexDigits, r) {
			return false
		}
	}
	return strings.IndexFunc(randomPart, func(r rune) bool {
		return !strings.ContainsRune(idCharset, r)
	}) == -1
}

// decodeTimestamp reverses the hex prefix: the id encodes
// ~(timestamp<<12 | counter), so inverting and shifting gives back the
// millisecond clock.
func decodeTimestamp(t *testing.T, hexPart string) int64 {
	t.Helper()

	v, err := strconv.ParseUint(hexPart, 16, 64)
	if err != nil {
		t.Fatalf("hex prefix %q: %v", hexPart, err)
	}

	return int64(^v >> idCounterShift)
}

// TestIDPrefixDecodesToRecentTimestamp pins the property the gateway actually
// leans on. A plain-random id would satisfy the shape assertions above and
// still be refused with a 403 FreeTierError, so the prefix has to decode to
// roughly now.
func TestIDPrefixDecodesToRecentTimestamp(t *testing.T) {
	for i := 0; i < 50; i++ {
		id := strings.TrimPrefix(NewSessionID(), "ses_")
		drift := time.Since(time.UnixMilli(decodeTimestamp(t, id[:idHexBytes*2])))
		if drift < 0 {
			drift = -drift
		}
		if drift > time.Minute {
			t.Fatalf("id %q decodes %dms from now, want it close to now", id, drift.Milliseconds())
		}
	}
}

func TestNewSessionIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := NewSessionID()
		if !strings.HasPrefix(id, "ses_") {
			t.Fatalf("id = %q, want ses_ prefix", id)
		}
		if !idSuffixValid(strings.TrimPrefix(id, "ses_")) {
			t.Fatalf("id = %q, want %d chars as %d hex + %d base62 after the prefix",
				id, idLength, idHexBytes*2, idRandom)
		}
	}
}

func TestNewRequestIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := NewRequestID()
		if !strings.HasPrefix(id, "msg_") {
			t.Fatalf("id = %q, want msg_ prefix", id)
		}
		if !idSuffixValid(strings.TrimPrefix(id, "msg_")) {
			t.Fatalf("id = %q, want %d chars as %d hex + %d base62 after the prefix",
				id, idLength, idHexBytes*2, idRandom)
		}
	}
}

func TestIDsAreUniqueAcrossCalls(t *testing.T) {
	seen := make(map[string]bool)

	for i := 0; i < 1000; i++ {
		for _, id := range []string{NewSessionID(), NewRequestID()} {
			if seen[id] {
				t.Fatalf("duplicate id %q after %d calls", id, i)
			}
			seen[id] = true
		}
	}
}

func TestSetHeaders(t *testing.T) {
	h := make(http.Header)

	SetHeaders(h, "ses_abc", "msg_def", "")

	want := map[string]string{
		"User-Agent":            UserAgent,
		"X-Opencode-Session":    "ses_abc",
		"X-Session-Id":          "ses_abc",
		"X-Opencode-Session-Id": "ses_abc",
		"X-Opencode-Request":    "msg_def",
		"X-Opencode-Project":    "global",
		"X-Opencode-Client":     "cli",
	}
	if len(h) != len(want) {
		t.Fatalf("headers = %v, want exactly %d entries", h, len(want))
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
	if _, ok := h["Authorization"]; ok {
		t.Fatalf("Authorization header set, want none")
	}
	if _, ok := h["X-Org-Id"]; ok {
		t.Fatalf("X-Org-Id header set, want none")
	}
}

func TestSetHeadersWithOrg(t *testing.T) {
	h := make(http.Header)

	SetHeaders(h, "ses_abc", "msg_def", "org-1")

	if got := h.Get("X-Org-Id"); got != "org-1" {
		t.Fatalf("X-Org-Id = %q, want org-1", got)
	}
}

func TestRequiredToolsMatchNames(t *testing.T) {
	names := RequiredToolNames()

	want := []string{"bash", "read", "task", "todowrite", "webfetch", "websearch", "write"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("names[%d] = %q, want %q", i, names[i], name)
		}
	}

	tools := RequiredTools()
	if len(tools) != len(names) {
		t.Fatalf("got %d tools, want %d", len(tools), len(names))
	}
	for i, name := range names {
		tool := tools[i]
		if tool["type"] != "function" {
			t.Fatalf("tool %d type = %v, want function", i, tool["type"])
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("tool %d function = %v, want map", i, tool["function"])
		}
		if fn["name"] != name {
			t.Fatalf("tool %d name = %v, want %q", i, fn["name"], name)
		}
		if fn["description"] != "DONT USE THIS TOOL" {
			t.Fatalf("tool %d description = %v", i, fn["description"])
		}
		if fn["strict"] != false {
			t.Fatalf("tool %d strict = %v, want false", i, fn["strict"])
		}
		params, ok := fn["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("tool %d parameters = %v, want map", i, fn["parameters"])
		}
		if len(params) != 3 {
			t.Fatalf("tool %d parameters = %v, want properties/required/type only", i, params)
		}
		props, ok := params["properties"].(map[string]any)
		if !ok || len(props) != 0 {
			t.Fatalf("tool %d properties = %v, want empty map", i, params["properties"])
		}
		req, ok := params["required"].([]string)
		if !ok || len(req) != 0 {
			t.Fatalf("tool %d required = %v, want empty slice", i, params["required"])
		}
		if params["type"] != "object" {
			t.Fatalf("tool %d parameters type = %v, want object", i, params["type"])
		}
	}
}

func TestRequiredToolsReturnsIndependentCopies(t *testing.T) {
	first := RequiredTools()

	first[0]["type"] = "mutated"
	fn := first[0]["function"].(map[string]any)
	fn["name"] = "mutated"
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	props["injected"] = true
	params["required"] = []string{"injected"}

	second := RequiredTools()
	if second[0]["type"] != "function" {
		t.Fatalf("second call type = %v, want function", second[0]["type"])
	}
	fn2, ok := second[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("second call function not a map")
	}
	if fn2["name"] != "bash" {
		t.Fatalf("second call name = %v, want bash", fn2["name"])
	}
	params2 := fn2["parameters"].(map[string]any)
	props2 := params2["properties"].(map[string]any)
	if len(props2) != 0 {
		t.Fatalf("second call properties = %v, want empty map", props2)
	}
	if req2 := params2["required"].([]string); len(req2) != 0 {
		t.Fatalf("second call required = %v, want empty slice", req2)
	}
}
