package opencode

import (
	"crypto/rand"
	"net/http"
)

const UserAgent = "opencode/latest/2.0.23/cli"

const (
	// The gateway validates the session/request id shape: 26 lowercase hex
	// characters after the prefix. Anything else (mixed case, dashes) fails
	// the free-tier client check with a 403.
	idLength = 26

	idCharset = "0123456789abcdef"
)

func RequiredToolNames() []string {
	return []string{"bash", "read", "task", "todowrite", "webfetch", "websearch", "write"}
}

func RequiredTools() []map[string]any {
	names := RequiredToolNames()
	tools := make([]map[string]any, len(names))
	for i, name := range names {
		tools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": "DONT USE THIS TOOL",
				"parameters": map[string]any{
					"properties": map[string]any{},
					"required":   []string{},
					"type":       "object",
				},
				"strict": false,
			},
		}
	}
	return tools
}

func NewSessionID() string {
	return newID("ses_")
}

func NewRequestID() string {
	return newID("msg_")
}

func SetHeaders(header http.Header, sessionID, requestID string) {
	header.Set("User-Agent", UserAgent)
	header.Set("x-opencode-session", sessionID)
	// Alternate spellings of the session header seen on the wire; the
	// gateway keys off whichever it knows and ignores the rest.
	header.Set("x-session-id", sessionID)
	header.Set("x-opencode-session-id", sessionID)
	header.Set("x-opencode-request", requestID)
	header.Set("x-opencode-project", "global")
	header.Set("x-opencode-client", "cli")
}

func newID(prefix string) string {
	buf := make([]byte, idLength)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	// 256 % 16 == 0, so the modulo stays unbiased.
	for i := range buf {
		buf[i] = idCharset[int(buf[i])%len(idCharset)]
	}

	return prefix + string(buf)
}
