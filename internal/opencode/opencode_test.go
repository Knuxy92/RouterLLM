package opencode

import (
	"net/http"
	"strings"
	"testing"
)

func idSuffixValid(s string) bool {
	if len(s) != idLength {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !strings.ContainsRune(idCharset, r)
	}) == -1
}

func TestNewSessionIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := NewSessionID()
		if !strings.HasPrefix(id, "ses_") {
			t.Fatalf("id = %q, want ses_ prefix", id)
		}
		if !idSuffixValid(strings.TrimPrefix(id, "ses_")) {
			t.Fatalf("id = %q, want %d chars from %q after prefix", id, idLength, idCharset)
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
			t.Fatalf("id = %q, want %d chars from %q after prefix", id, idLength, idCharset)
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

	SetHeaders(h, "ses_abc", "msg_def")

	want := map[string]string{
		"User-Agent":         UserAgent,
		"X-Opencode-Session": "ses_abc",
		"X-Opencode-Request": "msg_def",
		"X-Opencode-Project": "global",
		"X-Opencode-Client":  "cli",
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
