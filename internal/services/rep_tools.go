package services

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Representative-tool mode: the gateway's structural gate flags free-tier
// requests whose tools[] array does not look like the real CLI's — a client
// exposing its MCP servers directly (tens or hundreds of declarations) draws
// a soft 429 FreeUsageLimitError. Lab-verified fix: collapse the client's
// declarations into a text catalog inside the system prompt and expose ONE
// representative `execute` tool whose arguments are predictable JSON
// ({"tool", "arguments"}). The model calls the representative; the response
// side maps each call back to the client's native tool name, so any
// OpenAI-compatible client works unchanged.
//
// The transform is stateless in both directions: the catalog name IS the
// client's native name (verbatim), and history is re-collapsed on every
// request. Clients that already speak Code Mode (their tools[] carries its
// own `execute`) are detected and skipped, and the response side rewrites
// only arguments that parse as the representative shape — a CLI `execute`
// call carrying {"code": ...} passes through untouched.

// envRepTools switches the mode on; the same env-gated pattern as the TLS relay.
const envRepTools = "OPENCODE_REP_TOOLS"

// repToolsEnabled reports whether representative-tool mode is switched on.
func repToolsEnabled() bool {
	return os.Getenv(envRepTools) == "1"
}

// repToolName is the representative tool's name: the whitelisted CLI name the
// gateway's structural gate expects, carrying OUR schema.
const repToolName = "execute"

// repToolDef renders the representative tool in chat shape. injectMissingTools
// appends the four required CLI definitions next to it, giving the request a
// CLI-like pack — lab probe: rep + edit/read/shell/write passes the gate.
func repToolDef() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": repToolName,
			"description": "Call a tool from the Tool catalog. Pass {\"tool\": \"<catalog tool name>\", " +
				"\"arguments\": {<the tool's argument object>}}. One catalog tool per call; issue " +
				"several calls in the same response to run tools in parallel.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":      map[string]any{"type": "string", "description": "Catalog tool name, verbatim"},
					"arguments": map[string]any{"type": "object", "description": "The catalog tool's argument object"},
				},
				"required":             []string{"tool", "arguments"},
				"additionalProperties": false,
			},
			"strict": false,
		},
	}
}

// collapseRepTools applies the representative transform to one outbound body:
// the client's declarations become a catalog appended to the leading system
// message, history tool_calls are re-wrapped as execute calls, and tools[]
// shrinks to the representative definition (the required CLI set is injected
// later by injectMissingTools). It returns the collapsed declaration count and
// whether the transform applied; false leaves the body untouched.
func collapseRepTools(body map[string]any) (int, bool) {
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return 0, false
	}
	if clientForbidsToolUse(body) {
		return 0, false
	}
	// A client that already speaks Code Mode sends its own execute tool; leave
	// its whole request in the CLI's native shape.
	for _, entry := range tools {
		if name, ok := toolComparableName(entry); ok && strings.EqualFold(name, repToolName) {
			return 0, false
		}
	}

	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return 0, false
	}

	catalog := buildRepCatalog(tools)
	body["messages"] = appendRepCatalog(msgs, catalog)
	collapseRepHistory(msgs)
	body["tools"] = []any{repToolDef()}

	return len(tools), true
}

// appendRepCatalog appends the catalog text to the leading system message —
// the harness prompt injectOpenCodeSystemPrompt just prepended, so the
// gateway's <env> block stays intact. Without a leading system message the
// catalog becomes its own system message.
func appendRepCatalog(msgs []any, catalog string) []any {
	if len(msgs) > 0 {
		if first, ok := msgs[0].(map[string]any); ok {
			if role, _ := first["role"].(string); role == "system" {
				if content, ok := first["content"].(string); ok {
					first["content"] = content + "\n\n" + catalog
					return msgs
				}
			}
		}
	}

	return append([]any{map[string]any{"role": "system", "content": catalog}}, msgs...)
}

// collapseRepHistory re-wraps native history tool_calls into representative
// execute calls, mirroring the declarations the client would have received
// from the response side. role:"tool" results are left as-is: their
// tool_call_id links survive and the gateway accepts them unchanged.
func collapseRepHistory(msgs []any) {
	for _, entry := range msgs {
		msg, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		calls, ok := msg["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, call := range calls {
			// Chat-shaped tool_call: {id, type, function: {name, arguments}}.
			callMap, ok := call.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := callMap["function"].(map[string]any)
			if !ok {
				continue
			}
			name, _ := fn["name"].(string)
			if name == "" || name == repToolName {
				continue
			}
			wrapper := map[string]any{"tool": name, "arguments": map[string]any{}}
			if raw, _ := fn["arguments"].(string); raw != "" {
				var args map[string]any
				if json.Unmarshal([]byte(raw), &args) == nil {
					wrapper["arguments"] = args
				}
			}
			wrapped, err := json.Marshal(wrapper)
			if err != nil {
				continue
			}
			fn["name"] = repToolName
			fn["arguments"] = string(wrapped)
		}
	}
}

// buildRepCatalog renders the client's declarations as the text catalog the
// system prompt carries: one line per tool — name, parameter types, and a
// truncated description — plus the calling rules. Catalog names are the
// client's native names verbatim, which is what makes the response side
// stateless.
func buildRepCatalog(tools []any) string {
	var b strings.Builder
	b.WriteString("## Tool catalog\n\n")
	for _, entry := range tools {
		decl, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := decl["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s(%s)", name, repParamSignature(fn))
		if desc := repDescription(fn["description"]); desc != "" {
			b.WriteString(" — " + desc)
		}
		b.WriteByte('\n')
	}
	b.WriteString(`
## Calling tools

Every catalog tool is called through the single ` + "`execute`" + ` tool with JSON arguments:

    {"tool": "<catalog name>", "arguments": { ... }}

One ` + "`execute`" + ` call per tool call. To run several tools in parallel, return several
` + "`execute`" + ` tool calls in the same response. Only tools listed in the Tool catalog are
callable — every other tool definition in this request is a placeholder; never call it.`)

	return b.String()
}

// repParamSignature renders the parameters as "name: type" pairs, required
// ones marked with a star.
func repParamSignature(fn map[string]any) string {
	params, _ := fn["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}
	required := map[string]bool{}
	if req, ok := params["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}
	parts := make([]string, 0, len(props))
	for name, spec := range props {
		typeName := ""
		if m, ok := spec.(map[string]any); ok {
			typeName, _ = m["type"].(string)
		}
		if required[name] {
			name += "*"
		}
		if typeName == "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+": "+typeName)
		}
	}

	return strings.Join(parts, ", ")
}

// repDescription collapses a description to one line and caps it.
func repDescription(desc any) string {
	text, _ := desc.(string)
	text = strings.Join(strings.Fields(text), " ")
	const max = 160
	if len(text) > max {
		text = text[:max] + "…"
	}

	return text
}
