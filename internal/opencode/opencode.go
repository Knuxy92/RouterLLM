package opencode

import (
	"net/http"
)

const UserAgent = "opencode/latest/2.0.23/cli"

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

// NewSessionID returns the session header value. The gateway validates its
// shape, so it comes from newIdentifier rather than random hex.
func NewSessionID() string {
	return newIdentifier("ses_")
}

// NewRequestID returns the per-attempt header value, same shape as the session.
func NewRequestID() string {
	return newIdentifier("msg_")
}

func SetHeaders(header http.Header, sessionID, requestID, orgID string) {
	header.Set("User-Agent", UserAgent)
	header.Set("x-opencode-session", sessionID)
	// Alternate spellings of the session header seen on the wire; the
	// gateway keys off whichever it knows and ignores the rest.
	header.Set("x-session-id", sessionID)
	header.Set("x-opencode-session-id", sessionID)
	header.Set("x-opencode-request", requestID)
	header.Set("x-opencode-project", "global")
	header.Set("x-opencode-client", "cli")
	// The Console client attaches the serving account's org only when the
	// account belongs to one; the gateway behaves the same without it.
	if orgID != "" {
		header.Set("x-org-id", orgID)
	}
}
