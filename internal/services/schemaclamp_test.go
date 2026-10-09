package services

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func nestedSchema(levels int) map[string]any {
	if levels == 0 {
		return map[string]any{"type": "string"}
	}

	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"level" + string(rune('A'+levels)): nestedSchema(levels - 1)},
	}
}

func chatToolBody(name string, parameters map[string]any) map[string]any {
	return map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": "probe",
				"parameters":  parameters,
			},
		}},
	}
}

func toolParameters(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want exactly one tool", body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %v, want a map", tools[0])
	}
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatalf("tool.function = %v, want a map", tool["function"])
	}
	schema, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("tool.function.parameters = %v, want a map", fn["parameters"])
	}

	return schema
}

func TestSchemaDepthCountsNestedLevels(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]any
		want   int
	}{
		{name: "leaf", schema: map[string]any{"type": "string"}, want: 1},
		{name: "object with leaf property", schema: nestedSchema(1), want: 2},
		{name: "three nested objects", schema: nestedSchema(3), want: 4},
		{
			name: "array items count as a level",
			schema: map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			},
			want: 3,
		},
		{
			name:   "tuple items count as a level",
			schema: map[string]any{"type": "array", "items": []any{map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}}},
			want:   3,
		},
		{
			name: "anyOf branch counts as a level",
			schema: map[string]any{
				"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}},
			},
			want: 3,
		},
		{name: "empty schema is a leaf", schema: map[string]any{}, want: 1},
	}

	for _, tc := range cases {
		if got := schemaDepth(tc.schema); got != tc.want {
			t.Errorf("%s: schemaDepth = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestClampToolSchemaDepthsLeavesShallowToolsUntouched(t *testing.T) {
	body := chatToolBody("shallow", nestedSchema(3))

	before, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}

	if touched := clampToolSchemaDepths(body, defaultToolSchemaMaxDepth); len(touched) != 0 {
		t.Fatalf("clamped = %v, want none", touched)
	}

	after, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("shallow schema was modified:\nbefore %s\nafter  %s", before, after)
	}
}

func TestClampToolSchemaDepthsFlattensDeepChatTool(t *testing.T) {
	body := chatToolBody("deep", nestedSchema(12))

	touched := clampToolSchemaDepths(body, defaultToolSchemaMaxDepth)
	if !reflect.DeepEqual(touched, []string{"deep"}) {
		t.Fatalf("clamped = %v, want [deep]", touched)
	}

	schema := toolParameters(t, body)
	if got := schemaDepth(schema); got != defaultToolSchemaMaxDepth {
		t.Fatalf("clamped depth = %d, want exactly %d", got, defaultToolSchemaMaxDepth)
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 1 {
		t.Fatalf("root properties = %v, want the original property names kept", schema["properties"])
	}
	for name, child := range properties {
		childSchema, ok := child.(map[string]any)
		if !ok {
			t.Fatalf("property %q = %v, want a map", name, child)
		}
		if childSchema["type"] != "object" {
			t.Errorf("property %q type = %v, want object", name, childSchema["type"])
		}
		if _, ok := childSchema["properties"]; !ok {
			t.Errorf("property %q lost the chain down to the budget: %v", name, childSchema)
		}
	}

	stub := findFlattenedStub(schema)
	if stub == nil {
		t.Fatal("no flattened stub found in the clamped schema")
	}
	if stub["type"] != "object" {
		t.Errorf("stub type = %v, want object", stub["type"])
	}
	if description, _ := stub["description"].(string); description == "" {
		t.Errorf("stub has no description: %v", stub)
	}
}

// findFlattenedStub returns the first node the clamp replaced, searching the
// same child keys the clamp walks.
func findFlattenedStub(node map[string]any) map[string]any {
	if description, _ := node["description"].(string); strings.Contains(description, "flattened") {
		return node
	}

	for _, key := range schemaChildKeys {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for _, child := range children {
			if childNode, ok := child.(map[string]any); ok {
				if found := findFlattenedStub(childNode); found != nil {
					return found
				}
			}
		}
	}

	for _, key := range schemaBranchKeys {
		branches, ok := node[key].([]any)
		if !ok {
			continue
		}
		for _, child := range branches {
			if childNode, ok := child.(map[string]any); ok {
				if found := findFlattenedStub(childNode); found != nil {
					return found
				}
			}
		}
	}

	return nil
}

func TestClampToolSchemaDepthsKeepsDescriptiveFields(t *testing.T) {
	parameters := map[string]any{
		"type":        "object",
		"properties":  map[string]any{"config": map[string]any{"type": "object", "description": "deep config", "properties": nestedSchema(10)}},
		"required":    []any{"config"},
		"additionalProperties": false,
	}

	body := chatToolBody("described", parameters)
	clampToolSchemaDepths(body, defaultToolSchemaMaxDepth)

	schema := toolParameters(t, body)
	config := schema["properties"].(map[string]any)["config"].(map[string]any)
	if config["description"] != "deep config" {
		t.Errorf("description = %v, want the upstream description kept", config["description"])
	}
	if config["type"] != "object" {
		t.Errorf("type = %v, want object", config["type"])
	}
	if _, ok := schema["required"]; !ok {
		t.Error("clamp dropped the root required list")
	}
}

func TestClampToolSchemaDepthsHandlesFlatResponsesToolShape(t *testing.T) {
	body := map[string]any{
		"model": "test-model",
		"tools": []any{map[string]any{
			"type":       "function",
			"name":       "flat_deep",
			"parameters": nestedSchema(12),
		}},
	}

	touched := clampToolSchemaDepths(body, defaultToolSchemaMaxDepth)
	if !reflect.DeepEqual(touched, []string{"flat_deep"}) {
		t.Fatalf("clamped = %v, want [flat_deep]", touched)
	}

	tool := body["tools"].([]any)[0].(map[string]any)
	schema := tool["parameters"].(map[string]any)
	if got := schemaDepth(schema); got > defaultToolSchemaMaxDepth {
		t.Fatalf("clamped depth = %d, want <= %d", got, defaultToolSchemaMaxDepth)
	}
}

func TestClampToolSchemaDepthsSkipsWhenDisabled(t *testing.T) {
	body := chatToolBody("deep", nestedSchema(12))

	if touched := clampToolSchemaDepths(body, 0); len(touched) != 0 {
		t.Fatalf("clamped = %v, want none when the budget is zero", touched)
	}
	if got := schemaDepth(toolParameters(t, body)); got <= defaultToolSchemaMaxDepth {
		t.Fatalf("depth = %d, want the deep schema left intact when disabled", got)
	}
}

func TestForwardClampsAnthropicToolSchemasOnOpenCodeMessagesLeg(t *testing.T) {
	upstreamDepth := runAnthropicLeg(t, true)

	if upstreamDepth == 0 {
		t.Fatal("client tool never reached the upstream messages payload")
	}
	if upstreamDepth > defaultToolSchemaMaxDepth {
		t.Fatalf("upstream input_schema depth = %d, want <= %d", upstreamDepth, defaultToolSchemaMaxDepth)
	}
}

// The clamp-off case proves the assertion above is not vacuous: without the
// clamp the very same schema reaches the upstream intact.
func TestForwardLeavesAnthropicToolSchemasWhenClampOff(t *testing.T) {
	if depth := runAnthropicLeg(t, false); depth <= defaultToolSchemaMaxDepth {
		t.Fatalf("upstream input_schema depth = %d, want the schema untouched while the clamp is off", depth)
	}
}

func runAnthropicLeg(t *testing.T, clamp bool) int {
	t.Helper()

	var upstreamDepth int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}

		for _, entry := range body["tools"].([]any) {
			tool, _ := entry.(map[string]any)
			if name, _ := tool["name"].(string); name == "deep_probe" {
				upstreamDepth = schemaDepth(tool["input_schema"])
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\"}}\n\n")
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ok\"}}\n\n")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	registry := provider.NewRegistry([]config.ProviderConfig{{
		Name:    "oc-clamp",
		BaseURL: upstream.URL,
		Style:   "opencode",
		Keys:    []string{"oc_sk_key"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "oc-clamp", Model: "up-model", StyleCall: "messages", ClampToolSchemas: clamp}},
	}}, time.Minute)

	proxy := NewProxy(registry, upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(marshalBody(t, chatToolBody("deep_probe", nestedSchema(12)))))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	return upstreamDepth
}

func clampTestRegistry(t *testing.T, upstreamURL string, clamp bool) *provider.Registry {
	t.Helper()

	return provider.NewRegistry([]config.ProviderConfig{{
		Name:    "clamp-test",
		BaseURL: upstreamURL,
		Style:   "openai",
		Keys:    []string{"sk-test"},
	}}, []model.Rule{{
		ModelID: "test-model",
		Routes:  []model.Spec{{Provider: "clamp-test", Model: "up-model", ClampToolSchemas: clamp}},
	}}, time.Minute)
}

func TestForwardClampsToolSchemaDepthOnLeg(t *testing.T) {
	var upstreamDepth int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}

		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("upstream tools = %d, want 1", len(tools))
		} else {
			tool := tools[0].(map[string]any)
			fn, _ := tool["function"].(map[string]any)
			if fn["name"] != "deep_probe" {
				t.Errorf("upstream tool name = %v, want deep_probe", fn["name"])
			}
			upstreamDepth = schemaDepth(fn["parameters"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(clampTestRegistry(t, upstream.URL, true), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(marshalBody(t, chatToolBody("deep_probe", nestedSchema(12)))))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if upstreamDepth > defaultToolSchemaMaxDepth {
		t.Fatalf("upstream schema depth = %d, want <= %d", upstreamDepth, defaultToolSchemaMaxDepth)
	}
}

func TestForwardLeavesToolSchemaDepthWhenClampOff(t *testing.T) {
	var upstreamDepth int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body decode: %v", err)
		}

		tool := body["tools"].([]any)[0].(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		upstreamDepth = schemaDepth(fn["parameters"])

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy := NewProxy(clampTestRegistry(t, upstream.URL, false), upstream.Client(), log.New(io.Discard, "", 0), false, false, false, false, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(marshalBody(t, chatToolBody("deep_probe", nestedSchema(12)))))
	w := httptest.NewRecorder()

	proxy.Forward("/v1/chat/completions", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if upstreamDepth <= defaultToolSchemaMaxDepth {
		t.Fatalf("upstream schema depth = %d, want the schema untouched while the clamp is off", upstreamDepth)
	}
}

func marshalBody(t *testing.T, body map[string]any) []byte {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	return data
}