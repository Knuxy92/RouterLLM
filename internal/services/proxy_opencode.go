package services

import (
	"context"
	"encoding/json"
	"fmt"

	"routerllm/internal/adapter"
	"routerllm/internal/opencode"
	"routerllm/internal/provider"
)

// openCodeTokenKeys lists the token-cap fields the OpenCode gateway floor
// applies to, across every inbound dialect.
var openCodeTokenKeys = []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}

// opencodeBindingKey is the server/org pair a pool key was first seen with.
// The pool keys are the original refresh tokens, but rotation re-files them
// under new strings — the proxy remembers the first binding so per-request
// store lookups keep working after the first rotation.
type opencodeBindingKey struct {
	server string
	orgID  string
}

// opencodeAccessToken mints (or reuses) the Console access token backing one
// account refresh token. Each account may point at its own Console server, so
// managers are keyed by server URL the way cline managers are keyed by base
// URL. The refresh token itself — not the short-lived access token — is the
// key-pool key, so cooldown state stays stable across rotations.
func (p *Proxy) opencodeAccessToken(ctx context.Context, server, refreshToken string, force bool) (string, error) {
	if server == "" {
		server = opencode.DefaultServer()
	}

	p.opencodeMu.Lock()
	manager, ok := p.opencodeManagers[server]
	if !ok {
		store, err := opencode.LoadAccountStore(opencode.DefaultAccountsPath())
		if err != nil {
			p.log.Printf("opencode accounts unavailable: %v", err)
			store = nil
		}
		manager = opencode.NewManager(&opencode.Client{HTTPClient: p.client, Server: server}, store)
		p.opencodeManagers[server] = manager
	}
	p.opencodeMu.Unlock()

	return manager.AccessToken(ctx, refreshToken, force)
}

// opencodeServerOrg resolves the Console server and org id for the account
// holding refreshToken. Both fall back to empty (server then defaults inside
// opencodeAccessToken, org then omits the header) when the store is missing.
// After the first rotation the file no longer contains the original refresh
// token, so the sticky map remembers the server/org pair that the first
// lookup returned.
func (p *Proxy) opencodeServerOrg(refreshToken string) (server, orgID string) {
	p.opencodeMu.Lock()
	if bound, ok := p.opencodeBinding[refreshToken]; ok {
		p.opencodeMu.Unlock()

		return bound.server, bound.orgID
	}
	p.opencodeMu.Unlock()

	store, err := opencode.LoadAccountStore(opencode.DefaultAccountsPath())
	if err != nil {
		return "", ""
	}

	server, orgID = store.Server(refreshToken), store.OrgID(refreshToken)
	if server != "" || orgID != "" {
		p.opencodeMu.Lock()
		if p.opencodeBinding == nil {
			p.opencodeBinding = make(map[string]opencodeBindingKey)
		}
		if _, ok := p.opencodeBinding[refreshToken]; !ok {
			p.opencodeBinding[refreshToken] = opencodeBindingKey{server: server, orgID: orgID}
		}
		p.opencodeMu.Unlock()
	}

	return server, orgID
}

// pruneOpencodeManagers drops per-server Console managers once no account in
// the store points at that server anymore. The store is the source of truth
// for live servers because accounts — not provider blocks — own the server
// URL. The proxy already holds the provider registry, so a manager keyed by
// an account server survives exactly while at least one opencode provider is
// configured; without any opencode provider every manager is dropped.
func (p *Proxy) pruneOpencodeManagers(reg *provider.Registry) {
	keep := false
	for _, pc := range reg.ProviderConfigs() {
		if pc.Style == "opencode" && !pc.Disabled {
			keep = true
			break
		}
	}

	p.opencodeMu.Lock()
	defer p.opencodeMu.Unlock()

	if !keep {
		p.opencodeManagers = make(map[string]*opencode.Manager)
	}
}

// translateOpenCodeRoute builds the outbound request for style: opencode legs
// whose dialect is responses (stylecall: responses — chat is the default).
// The gateway rejects requests without its own tool set, so the required tools
// are injected and client tools whose names collide with them are renamed
// (restored on the way back).
func (p *Proxy) translateOpenCodeRoute(route provider.Route, path string, routeBody map[string]any, forbidToolUse bool) (map[string]string, []byte, string, error) {
	if path == "/v1/responses" {
		applyLegacyDefaults(routeBody, route.Defaults)
		reverse := renameReservedResponsesNames(routeBody, openCodeReservedTools)
		injectMissingResponsesTools(routeBody, openCodeResponsesTools())
		if forbidToolUse {
			dropToolUse(routeBody)
		}
		routeBody["model"] = route.ModelName

		reqBody, err := json.Marshal(routeBody)

		return reverse, reqBody, path, err
	}

	canonicalizeReasoning(routeBody, route.Defaults)
	reverse := renameReservedToolNames(routeBody, openCodeReservedTools)
	injectMissingTools(routeBody, opencode.RequiredTools())
	if forbidToolUse {
		dropToolUse(routeBody)
	}
	routeBody["model"] = route.ModelName

	reqBody, reqPath, err := adapter.TranslateResponsesRequest(routeBody, route.ModelName)

	return reverse, reqBody, reqPath, err
}

// clientForbidsToolUse reports whether the client sent tool_choice "none".
func clientForbidsToolUse(routeBody map[string]any) bool {
	choice, ok := routeBody["tool_choice"].(string)

	return ok && choice == "none"
}

// forceAutoToolChoice rewrites the tool_choice values the OpenCode gateway
// rejects — everything except "auto", and "none" which is honoured by removing
// the tools instead. It returns a description of what it replaced for the log
// line, or an empty string when nothing needed rewriting.
func forceAutoToolChoice(routeBody map[string]any) string {
	choice, ok := routeBody["tool_choice"]
	if !ok {
		return ""
	}
	if text, isText := choice.(string); isText && (text == "auto" || text == "none") {
		return ""
	}

	routeBody["tool_choice"] = "auto"

	return fmt.Sprintf("%v", choice)
}

// dropToolUse removes the tool surface entirely — the only way to honour
// tool_choice "none" on a gateway that accepts nothing but "auto".
func dropToolUse(body map[string]any) {
	delete(body, "tools")
	delete(body, "tool_choice")
}

// openCodeReservedTools holds the tool names the OpenCode gateway injects
// itself; client tools using them are renamed per request.
var openCodeReservedTools = func() map[string]bool {
	reserved := make(map[string]bool)
	for _, name := range opencode.RequiredToolNames() {
		reserved[name] = true
	}

	return reserved
}()

// openCodeResponsesTools returns the required tools in the flat Responses
// shape, fresh maps each call.
func openCodeResponsesTools() []map[string]any {
	chat := opencode.RequiredTools()
	flat := make([]map[string]any, 0, len(chat))
	for _, entry := range chat {
		if tool, ok := chatToolToResponsesTool(entry); ok {
			flat = append(flat, tool)
		}
	}

	return flat
}

// openCodeAnthropicTools returns the required tools in the Anthropic
// definition shape, fresh maps each call.
func openCodeAnthropicTools() []map[string]any {
	chat := opencode.RequiredTools()
	anthropic := make([]map[string]any, 0, len(chat))
	for _, entry := range chat {
		if tool, ok := chatToolToAnthropicTool(entry); ok {
			anthropic = append(anthropic, tool)
		}
	}

	return anthropic
}

// openCodeMinMaxTokens is the gateway's floor: max token caps below it are
// rejected with "The number must be `>= 16`" (the admin test panel's clamp
// happily lets 1-15 through).
const openCodeMinMaxTokens = 16

// clampOpenCodeMaxTokens raises any present token-cap key to the gateway floor.
func clampOpenCodeMaxTokens(body map[string]any, keys ...string) {
	for _, key := range keys {
		if v, ok := body[key].(float64); ok && v < openCodeMinMaxTokens {
			body[key] = float64(openCodeMinMaxTokens)
		}
	}
}

// mergeToolNameRestore folds a second reverse map into the accumulated one.
func mergeToolNameRestore(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if len(dst) == 0 {
		return src
	}
	for k, v := range src {
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}

	return dst
}
