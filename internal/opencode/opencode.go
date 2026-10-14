package opencode

import (
	"net/http"
)

const UserAgent = "opencode/latest/2.0.23/cli"

// ToolDef describes one tool the gateway requires on every dialect. Desc is
// the description the client would send for its own tool of that name, so the
// upstream model is not told the definition is a placeholder.
type ToolDef struct {
	Name       string
	Desc       string
	Properties []string
	Required   []string
}

// requiredTools is the single source of truth for the gateway's tool set.
// RequiredToolNames and RequiredTools both read it, so the names a client tool
// is renamed away from can never drift from the definitions injected in.
var requiredTools = []ToolDef{
	{
		Name:       "edit",
		Desc:       "Edit the contents of a file by finding and replacing exact text. DONT USE THIS TOOL, ERROR TOOL",
		Properties: []string{"oldString", "newString", "replaceAll"},
		Required:   []string{"path", "oldString", "newString"},
	},
	{
		Name:       "read",
		Desc:       "Read the contents of a file or directory. DONT USE THIS TOOL, ERROR TOOL",
		Properties: []string{"path", "offset", "limit"},
		Required:   []string{"path"},
	},
	{
		Name:       "shell",
		Desc:       "Execute a shell command and return its output. DONT USE THIS TOOL, ERROR TOOL",
		Properties: []string{"command", "workdir", "timeout", "background"},
		Required:   []string{"command"},
	},
	{
		Name:       "write",
		Desc:       "Writes a file to the local filesystem. DONT USE THIS TOOL, ERROR TOOL",
		Properties: []string{"path", "content"},
		Required:   []string{"path", "content"},
	},
}

// RequiredToolNames is the reserved-name set: a client tool colliding with one
// of these is renamed for the upstream call and mapped back on the response.
func RequiredToolNames() []string {
	names := make([]string, 0, len(requiredTools))
	for _, t := range requiredTools {
		names = append(names, t.Name)
	}

	return names
}

// RequiredToolDetail exposes the table itself so callers can read a tool's
// schema, not just the rendered definition.
func RequiredToolDetail() []ToolDef {
	return append([]ToolDef(nil), requiredTools...)
}

func RequiredTools() []map[string]any {
	tools := make([]map[string]any, len(requiredTools))
	for i, t := range requiredTools {
		props := make(map[string]any, len(t.Properties))
		for _, p := range t.Properties {
			props[p] = map[string]any{}
		}

		tools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Desc,
				"parameters": map[string]any{
					"type":                 "null",
					"properties":           props,
					"required":             t.Required,
					"additionalProperties": false,
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
