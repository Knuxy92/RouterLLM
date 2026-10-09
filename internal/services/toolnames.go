package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"routerllm/internal/adapter"
	"routerllm/internal/model"
)

const toolNameMaxLength = 64

func toolComparableName(entry any) (string, bool) {
	tm, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}

	if t, _ := tm["type"].(string); t != "" && t != "function" {
		return "", false
	}

	if fn, ok := tm["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); name != "" {
			return name, true
		}

		return "", false
	}

	if name, _ := tm["name"].(string); name != "" {
		return name, true
	}

	return "", false
}

func setToolDeclarationName(entry any, forward map[string]string) {
	tm, ok := entry.(map[string]any)
	if !ok {
		return
	}

	if t, _ := tm["type"].(string); t != "" && t != "function" {
		return
	}

	if fn, ok := tm["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); name != "" {
			if san, ok := forward[name]; ok {
				fn["name"] = san
			}
		}

		return
	}

	if name, _ := tm["name"].(string); name != "" {
		if san, ok := forward[name]; ok {
			tm["name"] = san
		}
	}
}

func dedupeTools(body map[string]any) int {
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return 0
	}

	seen := make(map[string]bool, len(tools))

	kept := make([]any, 0, len(tools))

	dropped := 0

	for _, entry := range tools {
		name, ok := toolComparableName(entry)
		if !ok {
			kept = append(kept, entry)
			continue
		}

		if seen[name] {
			dropped++
			continue
		}

		seen[name] = true
		kept = append(kept, entry)
	}

	if dropped > 0 {
		body["tools"] = kept
	}

	return dropped
}

func sanitizeToolName(name string) string {
	if len(name) <= toolNameMaxLength {
		return name
	}

	sum := sha256.Sum256([]byte(name))

	return name[:56] + "-" + hex.EncodeToString(sum[:])[:7]
}

func uniqueSanitizedToolName(name string, used map[string]bool) string {
	candidate := sanitizeToolName(name)
	if !used[candidate] {
		return candidate
	}

	prefix := name
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}

	for i := 1; ; i++ {
		sum := sha256.Sum256([]byte(name + ":" + strconv.Itoa(i)))

		candidate = prefix + "-" + hex.EncodeToString(sum[:])[:23]
		if !used[candidate] {
			return candidate
		}
	}
}

func assignSanitizedToolName(name string, forward, reverse map[string]string, used map[string]bool) string {
	if san, ok := forward[name]; ok {
		return san
	}

	san := uniqueSanitizedToolName(name, used)
	forward[name] = san
	used[san] = true

	if san != name {
		reverse[san] = name
	}

	return san
}

func mapDeclarationToolNames(tools []any, forward, reverse map[string]string, used map[string]bool) {
	for _, entry := range tools {
		name, ok := toolComparableName(entry)
		if !ok {
			continue
		}

		if len(name) <= toolNameMaxLength {
			used[name] = true
			continue
		}

		if _, ok := forward[name]; ok {
			continue
		}

		assignSanitizedToolName(name, forward, reverse, used)
	}
}

func rewriteToolDeclarations(tools []any, forward map[string]string) {
	if len(forward) == 0 {
		return
	}

	for _, entry := range tools {
		setToolDeclarationName(entry, forward)
	}
}

func rewriteHistoryToolCalls(body map[string]any, forward, reverse map[string]string, used map[string]bool) {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return
	}

	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}

		calls, ok := msg["tool_calls"].([]any)
		if !ok {
			continue
		}

		for _, c := range calls {
			tc, ok := c.(map[string]any)
			if !ok {
				continue
			}

			fn, ok := tc["function"].(map[string]any)
			if !ok {
				continue
			}

			name, _ := fn["name"].(string)
			if name == "" {
				continue
			}

			if san, ok := forward[name]; ok {
				fn["name"] = san
				continue
			}

			if len(name) > toolNameMaxLength {
				fn["name"] = assignSanitizedToolName(name, forward, reverse, used)
			}
		}
	}
}

func rewriteNamedFunctionName(fn map[string]any, forward, reverse map[string]string, used map[string]bool) {
	name, _ := fn["name"].(string)
	if name == "" {
		return
	}

	if san, ok := forward[name]; ok {
		fn["name"] = san
		return
	}

	if len(name) > toolNameMaxLength {
		fn["name"] = assignSanitizedToolName(name, forward, reverse, used)
	}
}

func rewriteToolChoice(body map[string]any, forward, reverse map[string]string, used map[string]bool) {
	tc, ok := body["tool_choice"].(map[string]any)
	if !ok {
		return
	}

	if t, _ := tc["type"].(string); t != "" && t != "function" {
		return
	}

	if fn, ok := tc["function"].(map[string]any); ok {
		rewriteNamedFunctionName(fn, forward, reverse, used)
		return
	}

	rewriteNamedFunctionName(tc, forward, reverse, used)
}

func rewriteAllowedFunctionNames(body map[string]any, forward, reverse map[string]string, used map[string]bool) {
	cfg, ok := body["toolConfig"].(map[string]any)
	if !ok {
		return
	}

	fcc, ok := cfg["functionCallingConfig"].(map[string]any)
	if !ok {
		return
	}

	allowed, ok := fcc["allowed_function_names"].([]any)
	if !ok {
		return
	}

	for i, v := range allowed {
		name, _ := v.(string)
		if name == "" {
			continue
		}

		if san, ok := forward[name]; ok {
			allowed[i] = san
			continue
		}

		if len(name) > toolNameMaxLength {
			allowed[i] = assignSanitizedToolName(name, forward, reverse, used)
		}
	}
}

func sanitizeToolNames(body map[string]any) map[string]string {
	forward := make(map[string]string)
	reverse := make(map[string]string)
	used := make(map[string]bool)

	tools, _ := body["tools"].([]any)
	mapDeclarationToolNames(tools, forward, reverse, used)
	rewriteToolDeclarations(tools, forward)

	rewriteHistoryToolCalls(body, forward, reverse, used)
	rewriteToolChoice(body, forward, reverse, used)
	rewriteAllowedFunctionNames(body, forward, reverse, used)

	return reverse
}

func processToolNames(body map[string]any, doDedupe, doSanitize bool) (map[string]string, int) {
	dropped := 0
	if doDedupe {
		dropped = dedupeTools(body)
	}

	if !doSanitize {
		return nil, dropped
	}

	return sanitizeToolNames(body), dropped
}

// restorePayloadNames maps sanitized tool names in one outbound SSE payload back
// to the client's originals. Two frame shapes are handled: OpenAI chat chunks
// (tool_calls[].function.name) and Anthropic frames (content_block.name for a
// tool_use block). The payload is returned untouched when nothing needs
// restoring.
func restorePayloadNames(payload string, reverse map[string]string) string {
	if len(reverse) == 0 {
		return payload
	}

	if strings.Contains(payload, `"content_block"`) {
		return restoreAnthropicFrameNames(payload, reverse)
	}
	if !strings.Contains(payload, `"tool_calls"`) {
		return payload
	}

	var chunk map[string]any
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return payload
	}

	choices, ok := chunk["choices"].([]any)
	if !ok {
		return payload
	}

	changed := false
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			continue
		}

		if delta, ok := choice["delta"].(map[string]any); ok && restoreToolCallsField(delta, reverse) {
			changed = true
		}
		if message, ok := choice["message"].(map[string]any); ok && restoreToolCallsField(message, reverse) {
			changed = true
		}
	}
	if !changed {
		return payload
	}

	encoded, err := json.Marshal(chunk)
	if err != nil {
		return payload
	}

	return string(encoded)
}

// restoreAnthropicFrameNames rewrites content_block.name on tool_use blocks,
// which is how the /v1/messages and force_stream writers publish a tool call.
func restoreAnthropicFrameNames(payload string, reverse map[string]string) string {
	var frame map[string]any
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		return payload
	}

	block, ok := frame["content_block"].(map[string]any)
	if !ok {
		return payload
	}
	if t, _ := block["type"].(string); t != "tool_use" {
		return payload
	}

	name, _ := block["name"].(string)
	orig, ok := reverse[name]
	if !ok {
		return payload
	}

	block["name"] = orig

	encoded, err := json.Marshal(frame)
	if err != nil {
		return payload
	}

	return string(encoded)
}

// restoreToolCallsField rewrites names inside one message/delta holder's
// tool_calls array. It reports whether anything changed.
func restoreToolCallsField(holder map[string]any, reverse map[string]string) bool {
	calls, ok := holder["tool_calls"].([]any)
	if !ok {
		return false
	}

	changed := false
	for _, c := range calls {
		call, ok := c.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := call["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if orig, ok := reverse[name]; ok {
			fn["name"] = orig
			changed = true
		}
	}

	return changed
}

// restoreToolCallNamesJSON restores names across every tool call in an already
// assembled buffered response body.
func restoreToolCallNamesJSON(body []byte, reverse map[string]string) []byte {
	if len(reverse) == 0 || len(body) == 0 || !strings.Contains(string(body), `"tool_calls"`) {
		return body
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}

	choices, ok := doc["choices"].([]any)
	if !ok {
		return body
	}

	changed := false
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if message, ok := choice["message"].(map[string]any); ok && restoreToolCallsField(message, reverse) {
			changed = true
		}
	}
	if !changed {
		return body
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		return body
	}

	return encoded
}

// restoreBufferedToolNames restores names on a typed buffered response, which is
// the shape bufferStream hands back for non-streaming clients.
func restoreBufferedToolNames(result *model.ChatCompletionResponse, reverse map[string]string) *model.ChatCompletionResponse {
	if len(reverse) == 0 || result == nil {
		return result
	}

	for i := range result.Choices {
		for j := range result.Choices[i].Message.ToolCalls {
			name := result.Choices[i].Message.ToolCalls[j].Function.Name
			if orig, ok := reverse[name]; ok {
				result.Choices[i].Message.ToolCalls[j].Function.Name = orig
			}
		}
	}

	return result
}

// streamRestoreTransform adapts the payload restorer to the SSE frame hook.
func streamRestoreTransform(reverse map[string]string) func(string) string {
	if len(reverse) == 0 {
		return nil
	}

	return func(payload string) string {
		return restorePayloadNames(payload, reverse)
	}
}

// restoringWriter rewrites tool names inside the complete SSE frames a
// converter emits before they reach the client. Writes that do not contain a
// whole frame pass through untouched, so a split frame degrades to "no
// restore" rather than corrupting the stream.
type restoringWriter struct {
	http.ResponseWriter
	reverse map[string]string
}

func newRestoringWriter(w http.ResponseWriter, reverse map[string]string) http.ResponseWriter {
	if len(reverse) == 0 {
		return w
	}

	return &restoringWriter{ResponseWriter: w, reverse: reverse}
}

// RestoreToolNamesWriter wraps w so SSE frames written through it hand clients
// back the tool names they originally sent. Returns w unchanged when the leg
// sanitized nothing.
func RestoreToolNamesWriter(w http.ResponseWriter, reverse map[string]string) http.ResponseWriter {
	return newRestoringWriter(w, reverse)
}

func (w *restoringWriter) Write(p []byte) (int, error) {
	payload := string(p)
	// Two frame shapes reach this writer: chat chunks from the converters, and
	// raw Anthropic frames on the pass-through paths (force_stream and
	// /v1/messages), which carry content_block instead of tool_calls.
	if !strings.Contains(payload, `"tool_calls"`) && !strings.Contains(payload, `"content_block"`) {
		return w.ResponseWriter.Write(p)
	}

	restored := restoreSSEFrames(payload, w.reverse)
	if _, err := w.ResponseWriter.Write([]byte(restored)); err != nil {
		return 0, err
	}

	return len(p), nil
}

// restoreSSEFrames applies the payload restorer to every complete data frame in
// one write, leaving everything else byte-identical.
func restoreSSEFrames(chunk string, reverse map[string]string) string {
	if !strings.Contains(chunk, "data: ") {
		return chunk
	}

	var b strings.Builder
	rest := chunk
	for {
		i := strings.Index(rest, "data: ")
		if i < 0 {
			b.WriteString(rest)

			return b.String()
		}
		end := strings.IndexByte(rest[i:], '\n')
		if end < 0 {
			b.WriteString(rest)

			return b.String()
		}

		b.WriteString(rest[:i+len("data: ")])
		payload := rest[i+len("data: ") : i+end]
		b.WriteString(restorePayloadNames(payload, reverse))
		b.WriteString(rest[i+end : i+end+1])
		rest = rest[i+end+1:]
	}
}

// renameReservedToolNames renames client tools whose names collide with the
// reserved set (chat shape: declarations, history tool_calls, tool_choice) and
// returns the sanitized->original map for response restore.
func renameReservedToolNames(body map[string]any, reserved map[string]bool) map[string]string {
	forward, reverse, used := newReservedRename(reserved)

	tools, _ := body["tools"].([]any)
	for _, entry := range tools {
		name, ok := toolComparableName(entry)
		if !ok || !reserved[name] {
			continue
		}
		assignSanitizedToolName(name, forward, reverse, used)
	}
	if len(forward) == 0 {
		return nil
	}

	rewriteToolDeclarations(tools, forward)
	rewriteHistoryToolCalls(body, forward, reverse, used)
	rewriteToolChoice(body, forward, reverse, used)

	return reverse
}

// renameReservedResponsesNames is the Responses-shape variant: it walks the
// whole document and renames function_call items, flat function declarations
// and chat-shaped tool_choice entries whose names collide with the reserved set.
func renameReservedResponsesNames(doc map[string]any, reserved map[string]bool) map[string]string {
	forward, reverse, used := newReservedRename(reserved)

	walkReserved(doc, reserved, forward, reverse, used)
	if len(forward) == 0 {
		return nil
	}

	return reverse
}

func newReservedRename(reserved map[string]bool) (map[string]string, map[string]string, map[string]bool) {
	forward := make(map[string]string)
	reverse := make(map[string]string)
	used := make(map[string]bool, len(reserved))
	for name := range reserved {
		used[name] = true
	}

	return forward, reverse, used
}

// walkReserved visits every object/array and renames names on function_call
// items, flat function declarations and chat-shaped tool_choice.
func walkReserved(v any, reserved map[string]bool, forward, reverse map[string]string, used map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		typeName, _ := t["type"].(string)
		name, _ := t["name"].(string)
		if (typeName == "function_call" || typeName == "function") && name != "" && reserved[name] {
			t["name"] = assignSanitizedToolName(name, forward, reverse, used)
		}
		if fn, ok := t["function"].(map[string]any); ok {
			walkReserved(fn, reserved, forward, reverse, used)
		}
		for _, child := range t {
			walkReserved(child, reserved, forward, reverse, used)
		}
	case []any:
		for _, child := range t {
			walkReserved(child, reserved, forward, reverse, used)
		}
	}
}

// injectMissingTools appends required tools (chat shape) the client did not
// send and defaults tool_choice to auto.
func injectMissingTools(body map[string]any, required []map[string]any) {
	if len(required) == 0 {
		return
	}

	tools, _ := body["tools"].([]any)
	existing := make(map[string]bool, len(tools))
	for _, entry := range tools {
		if name, ok := toolComparableName(entry); ok {
			existing[name] = true
		}
	}

	for _, tool := range required {
		name, _ := toolComparableName(tool)
		if name == "" || existing[name] {
			continue
		}
		tools = append(tools, tool)
	}
	body["tools"] = tools

	if _, ok := body["tool_choice"]; !ok {
		body["tool_choice"] = "auto"
	}
}

// injectMissingResponsesTools is the Responses-shape variant of
// injectMissingTools: required tools must already be flat.
func injectMissingResponsesTools(doc map[string]any, required []map[string]any) {
	if len(required) == 0 {
		return
	}

	tools, _ := doc["tools"].([]any)
	existing := make(map[string]bool, len(tools))
	for _, entry := range tools {
		if tm, ok := entry.(map[string]any); ok {
			if name, _ := tm["name"].(string); name != "" {
				existing[name] = true
			}
		}
	}

	for _, tool := range required {
		name, _ := tool["name"].(string)
		if name == "" || existing[name] {
			continue
		}
		tools = append(tools, tool)
	}
	doc["tools"] = tools

	if _, ok := doc["tool_choice"]; !ok {
		doc["tool_choice"] = "auto"
	}
}

// chatToolToResponsesTool flattens one chat-shaped function tool, preserving
// name/description/parameters/strict.
func chatToolToResponsesTool(entry map[string]any) (map[string]any, bool) {
	fn, ok := entry["function"].(map[string]any)
	if !ok {
		return nil, false
	}

	name, _ := fn["name"].(string)
	if name == "" {
		return nil, false
	}

	flat := map[string]any{"type": "function", "name": name}
	if d, ok := fn["description"].(string); ok {
		flat["description"] = d
	}
	if p, ok := fn["parameters"]; ok && p != nil {
		flat["parameters"] = p
	}
	if s, ok := fn["strict"].(bool); ok {
		flat["strict"] = s
	}

	return flat, true
}

// restoreResponsesPayloadNames maps sanitized tool names in one outbound
// Responses frame (or a buffered document) back to the client's originals.
func restoreResponsesPayloadNames(payload string, reverse map[string]string) string {
	if len(reverse) == 0 || !strings.Contains(payload, `"function_call"`) {
		return payload
	}

	var frame any
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		return payload
	}
	if !restoreFunctionCallNames(frame, reverse) {
		return payload
	}

	encoded, err := json.Marshal(frame)
	if err != nil {
		return payload
	}

	return string(encoded)
}

// restoreFunctionCallNames rewrites the name inside every function_call item,
// reporting whether anything changed.
func restoreFunctionCallNames(v any, reverse map[string]string) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		if typeName, _ := t["type"].(string); typeName == "function_call" {
			if name, _ := t["name"].(string); name != "" {
				if orig, ok := reverse[name]; ok {
					t["name"] = orig
					changed = true
				}
			}
		}
		for _, child := range t {
			if restoreFunctionCallNames(child, reverse) {
				changed = true
			}
		}
	case []any:
		for _, child := range t {
			if restoreFunctionCallNames(child, reverse) {
				changed = true
			}
		}
	}

	return changed
}

// injectMissingAnthropicTools appends required tools (Anthropic shape:
// {name, description, input_schema}) the body does not already carry.
func injectMissingAnthropicTools(doc map[string]any, required []map[string]any) {
	if len(required) == 0 {
		return
	}

	tools, _ := doc["tools"].([]any)
	existing := make(map[string]bool, len(tools))
	for _, entry := range tools {
		if tm, ok := entry.(map[string]any); ok {
			if name, _ := tm["name"].(string); name != "" {
				existing[name] = true
			}
		}
	}

	for _, tool := range required {
		name, _ := tool["name"].(string)
		if name == "" || existing[name] {
			continue
		}
		tools = append(tools, tool)
	}
	doc["tools"] = tools
}

// chatToolToAnthropicTool converts one chat-shaped function tool to the
// Anthropic definition shape. The conversion lives in the adapter because the
// request translator needs it too, and two implementations would drift.
func chatToolToAnthropicTool(entry map[string]any) (map[string]any, bool) {
	return adapter.ChatToolToAnthropicTool(entry)
}
