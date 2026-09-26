package opencode

import (
	"crypto/rand"
	"net/http"
)

const UserAgent = "opencode/1.18.32 ai-sdk/provider-utils/4.0.40 runtime/bun/1.3.14"

const (
	idLength = 24

	// Accept only bytes below this so each of the 62 charset symbols maps
	// from an equal number of byte values; 256 % 62 == 8, so the top 8
	// values are redrawn instead of biasing the modulo.
	maxRandByte = 256 - (256 % 62)

	idCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
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
	header.Set("x-opencode-request", requestID)
	header.Set("x-opencode-project", "global")
	header.Set("x-opencode-client", "cli")
}

func newID(prefix string) string {
	buf := make([]byte, idLength)
	for i := 0; i < idLength; {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		if b[0] >= maxRandByte {
			continue
		}
		buf[i] = idCharset[b[0]%62]
		i++
	}
	return prefix + string(buf)
}
