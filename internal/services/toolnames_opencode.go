package services

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
