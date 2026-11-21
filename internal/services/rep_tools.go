package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"routerllm/internal/model"
	"routerllm/internal/util"
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

// repUnwrapArguments reverses the representative wrapper: {"tool": "<native>",
// "arguments": {...}} becomes the native tool name and its re-marshaled
// argument object. Arguments that do not parse as the wrapper shape — the
// CLI's own execute tool carries {"code": ...} — return ok=false and pass
// through untouched, which keeps Code Mode clients safe while the mode is on.
func repUnwrapArguments(raw string) (native string, inner string, ok bool) {
	var wrapper struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil || wrapper.Tool == "" {
		return "", "", false
	}
	if len(wrapper.Arguments) == 0 || string(wrapper.Arguments) == "null" {
		wrapper.Arguments = json.RawMessage("{}")
	}

	return wrapper.Tool, string(wrapper.Arguments), true
}

// repRestoreBuffered unwraps representative execute calls in an already
// assembled response (bufferStream output) so a native client sees its own
// tool names.
func repRestoreBuffered(result *model.ChatCompletionResponse) *model.ChatCompletionResponse {
	for ci := range result.Choices {
		for ti := range result.Choices[ci].Message.ToolCalls {
			tc := &result.Choices[ci].Message.ToolCalls[ti]
			if tc.Function.Name != repToolName {
				continue
			}
			native, inner, ok := repUnwrapArguments(tc.Function.Arguments)
			if !ok {
				continue
			}
			tc.Function.Name = native
			tc.Function.Arguments = inner
		}
	}

	return result
}

// repPendingCall accumulates one representative tool call the upstream streams
// as fragments: the native name lives inside the arguments JSON, so the call
// can only be rewritten once its arguments are complete.
type repPendingCall struct {
	id   string
	args strings.Builder
}

// repStreamSSE forwards an openai-dialect stream under representative-tool
// mode. Content and reasoning deltas reach the client live; execute tool_call
// fragments are held per index until the finish frame, then re-emitted as
// complete native tool calls. Foreign tool_call names and arguments that do
// not parse as the wrapper pass through untouched. reverse (the sanitize
// rename map) is applied to every frame on the way out.
func repStreamSSE(src io.Reader, w http.ResponseWriter, reverse map[string]string) error {
	flusher, _ := w.(http.Flusher)
	write := func(frame string) {
		fmt.Fprintf(w, "data: %s\n\n", frame)
		if flusher != nil {
			flusher.Flush()
		}
	}

	pending := map[int]*repPendingCall{}
	restore := func(frame string) string {
		if len(reverse) == 0 {
			return frame
		}
		return restorePayloadNames(frame, reverse)
	}

	emitPending := func() {
		if len(pending) == 0 {
			return
		}
		indexes := make([]int, 0, len(pending))
		for idx := range pending {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		for _, idx := range indexes {
			call := pending[idx]
			name, inner := repToolName, call.args.String()
			if native, unwrapped, ok := repUnwrapArguments(call.args.String()); ok {
				name, inner = native, unwrapped
			}
			synthetic, err := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"index":    idx,
							"id":       call.id,
							"type":     "function",
							"function": map[string]any{"name": name, "arguments": inner},
						}},
					},
				}},
			})
			if err != nil {
				continue
			}
			write(restore(string(synthetic)))
		}
		pending = map[int]*repPendingCall{}
	}

	_, err := util.IterDataLines(src, func(payload string) bool {
		if _, isError := util.ParseErrorFrame(payload); isError {
			write(util.SanitizedErrorFrame)
			return true
		}

		var frame struct {
			Choices []struct {
				Delta struct {
					Content          string           `json:"content"`
					ReasoningContent string           `json:"reasoning_content"`
					ToolCalls        []model.ToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			write(restore(payload))
			return true
		}

		buffered := false
		kept := map[[2]int]bool{} // [choiceIdx, callIdx] -> passthrough as-is
		live := false             // the frame carries content/reasoning to emit now
		for ci := range frame.Choices {
			if frame.Choices[ci].Delta.Content != "" || frame.Choices[ci].Delta.ReasoningContent != "" {
				live = true
			}
			for ki, call := range frame.Choices[ci].Delta.ToolCalls {
				_, alreadyPending := pending[call.Index]
				if call.Function.Name == repToolName || alreadyPending {
					buffered = true
					pc, ok := pending[call.Index]
					if !ok {
						pc = &repPendingCall{}
						pending[call.Index] = pc
					}
					if call.ID != "" {
						pc.id = call.ID
					}
					pc.args.WriteString(call.Function.Arguments)
				} else {
					kept[[2]int{ci, ki}] = true
					live = true
				}
			}
		}
		finish := false
		for _, choice := range frame.Choices {
			if choice.FinishReason != nil {
				finish = true
				break
			}
		}
		// The calls must reach the client before the frame that ends them,
		// even when the finish frame carries nothing else.
		if finish {
			emitPending()
		}

		if buffered && live {
			// The frame mixes live content (or foreign calls) with buffered
			// fragments: re-emit only the parts that pass through now.
			var doc map[string]any
			if err := json.Unmarshal([]byte(payload), &doc); err == nil {
				if choices, ok := doc["choices"].([]any); ok {
					for ci, raw := range choices {
						choice, _ := raw.(map[string]any)
						delta, _ := choice["delta"].(map[string]any)
						calls, _ := delta["tool_calls"].([]any)
						filtered := make([]any, 0, len(calls))
						for ki, c := range calls {
							if kept[[2]int{ci, ki}] {
								filtered = append(filtered, c)
							}
						}
						if len(filtered) == 0 {
							delete(delta, "tool_calls")
						} else {
							delta["tool_calls"] = filtered
						}
					}
				}
				if out, err := json.Marshal(doc); err == nil {
					write(restore(string(out)))
				}
			}
		} else if !buffered {
			write(restore(payload))
		}

		return true
	})
	if err == nil {
		write("[DONE]")
	}

	return err
}
