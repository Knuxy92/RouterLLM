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
	table := RequiredToolDetail()

	if len(names) == 0 {
		t.Fatal("no required tools")
	}
	if len(table) != len(names) {
		t.Fatalf("table has %d tools, names has %d", len(table), len(names))
	}

	tools := RequiredTools()
	if len(tools) != len(names) {
		t.Fatalf("got %d tools, want %d", len(tools), len(names))
	}

	for i, name := range names {
		// The rename pass keys off RequiredToolNames and the injection pass
		// keys off RequiredTools. If these ever disagree, a colliding client
		// tool is renamed without its replacement ever being injected.
		want := table[i].Name
		if name != want {
			t.Fatalf("names[%d] = %q, want %q (table entry)", i, name, want)
		}

		tool := tools[i]
		if tool["type"] != "function" {
			t.Fatalf("tool %d type = %v, want function", i, tool["type"])
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("tool %d function = %v, want map", i, tool["function"])
		}
		if fn["name"] != want {
			t.Fatalf("tool %d name = %v, want %q", i, fn["name"], want)
		}
		if fn["description"] != table[i].Desc {
			t.Fatalf("tool %d description = %v, want %q", i, fn["description"], table[i].Desc)
		}
		if fn["strict"] != false {
			t.Fatalf("tool %d strict = %v, want false", i, fn["strict"])
		}

		params, ok := fn["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("tool %d parameters = %v, want map", i, fn["parameters"])
		}
		if len(params) != 4 {
			t.Fatalf("tool %d parameters = %v, want type/properties/required/additionalProperties", i, params)
		}
		props, ok := params["properties"].(map[string]any)
		if !ok || len(props) != len(table[i].Properties) {
			t.Fatalf("tool %d properties = %v, want %d entries", i, params["properties"], len(table[i].Properties))
		}
		for _, p := range table[i].Properties {
			if _, ok := props[p]; !ok {
				t.Fatalf("tool %d properties missing %q", i, p)
			}
		}
		if req, ok := params["required"].([]string); !ok || len(req) != len(table[i].Required) {
			t.Fatalf("tool %d required = %v, want %v", i, params["required"], table[i].Required)
		}
		if params["type"] != "null" {
			t.Fatalf("tool %d parameters type = %v, want null", i, params["type"])
		}
		if params["additionalProperties"] != false {
			t.Fatalf("tool %d additionalProperties = %v, want false", i, params["additionalProperties"])
		}
	}
}

func TestRequiredToolsReturnsIndependentCopies(t *testing.T) {
	table := RequiredToolDetail()
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
	if fn2["name"] != table[0].Name {
		t.Fatalf("second call name = %v, want %q", fn2["name"], table[0].Name)
	}
	params2 := fn2["parameters"].(map[string]any)
	props2 := params2["properties"].(map[string]any)
	if _, leaked := props2["injected"]; leaked {
		t.Fatalf("second call properties = %v, want the original set", props2)
	}
	if req2 := params2["required"].([]string); len(req2) != len(table[0].Required) {
		t.Fatalf("second call required = %v, want %v", req2, table[0].Required)
	}
}
