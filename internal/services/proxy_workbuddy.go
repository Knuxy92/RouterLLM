package services

import (
	"context"
	"encoding/json"
	"log"

	"routerllm/internal/provider"
	"routerllm/internal/workbuddy"
)

// workbuddyBindingKey is the realm/identity pair a pool key was first seen
// with. The pool keys are the original refresh tokens, but rotation re-files
// them under new strings — the proxy remembers the first binding so
// per-request store lookups keep working after the first rotation.
type workbuddyBindingKey struct {
	realm   string
	uid     string
	product string
}

// workbuddyAccount resolves the realm, uid and product identity of the
// account holding refreshToken. After the first rotation the account file no
// longer contains the original refresh token, so the sticky binding keeps the
// first lookup's answer.
func (p *Proxy) workbuddyAccount(refreshToken string) (realm, uid, product string) {
	p.workbuddyMu.Lock()
	if bound, ok := p.workbuddyBinding[refreshToken]; ok {
		p.workbuddyMu.Unlock()

		return bound.realm, bound.uid, bound.product
	}
	p.workbuddyMu.Unlock()

	store, err := workbuddy.LoadAccountStore(workbuddy.DefaultAccountsPath())
	if err != nil {
		log.Printf("workbuddy accounts unavailable: %v", err)
		return "intl", "", "desktop"
	}

	realm = store.Realm(refreshToken)
	if realm == "" {
		realm = "intl"
	}
	uid = store.UID(refreshToken)
	product = store.Product(refreshToken)
	if product == "" {
		product = "desktop"
	}

	p.workbuddyMu.Lock()
	if p.workbuddyBinding == nil {
		p.workbuddyBinding = make(map[string]workbuddyBindingKey)
	}
	if _, ok := p.workbuddyBinding[refreshToken]; !ok {
		p.workbuddyBinding[refreshToken] = workbuddyBindingKey{realm: realm, uid: uid, product: product}
	}
	p.workbuddyMu.Unlock()

	return realm, uid, product
}

// workbuddyAccessToken mints (or reuses) the access token backing one account
// refresh token. Managers are keyed by realm — the two realms are two
// independent upstreams with independent refresh endpoints.
func (p *Proxy) workbuddyAccessToken(ctx context.Context, realm, uid, refreshToken string, force bool) (string, error) {
	p.workbuddyMu.Lock()
	manager, ok := p.workbuddyManagers[realm]
	if !ok {
		store, err := workbuddy.LoadAccountStore(workbuddy.DefaultAccountsPath())
		if err != nil {
			log.Printf("workbuddy accounts unavailable: %v", err)
			store = nil
		}
		manager = workbuddy.NewManager(&workbuddy.Client{HTTPClient: p.client}, store)
		p.workbuddyManagers[realm] = manager
	}
	p.workbuddyMu.Unlock()

	return manager.AccessToken(ctx, realm, uid, refreshToken, force)
}

// pruneWorkbuddyManagers drops the per-realm refresh managers once no enabled
// workbuddy provider is configured.
func (p *Proxy) pruneWorkbuddyManagers(reg *provider.Registry) {
	keep := false
	for _, pc := range reg.ProviderConfigs() {
		if pc.Style == "workbuddy" && !pc.Disabled {
			keep = true
			break
		}
	}

	p.workbuddyMu.Lock()
	defer p.workbuddyMu.Unlock()

	if !keep {
		p.workbuddyManagers = make(map[string]*workbuddy.Manager)
	}
}

// defaultWorkbuddySystemPrompt is prepended when the client sent no system
// message: the upstream expects one first message to be a system turn.
const defaultWorkbuddySystemPrompt = "You are a helpful assistant."

// translateWorkbuddyRoute builds the outbound body for style: workbuddy legs.
// The upstream speaks OpenAI chat shape but enforces its own constraints:
// developer roles are rejected (11128), client-agent fingerprint sentences in
// system/assistant text trip the WAF (11-128), and DeepSeek thinking turns
// require reasoning_content on every assistant message.
func (p *Proxy) translateWorkbuddyRoute(route provider.Route, routeBody map[string]any) ([]byte, error) {
	if route.Provider.ReasoningStyle == "raw" {
		applyLegacyDefaults(routeBody, route.Defaults)
	} else {
		if notice := canonicalizeReasoning(routeBody, route.Defaults); notice != "" {
			p.log.Printf("route %s/%s: %s — ignored", route.ModelName, route.Provider.Name, notice)
		}
		applyReasoningDialect(routeBody, route.Provider.ReasoningStyle)
	}

	routeBody["model"] = route.ModelName
	routeBody["messages"] = workbuddy.NormalizeRoles(messagesOf(routeBody))
	routeBody["messages"] = workbuddy.SanitizeMessages(routeBody["messages"].([]any))
	routeBody["messages"] = workbuddy.BackfillReasoningContent(routeBody["messages"].([]any), route.ModelName, workbuddyThinkingEnabled(routeBody, route.ModelName))

	msgs, _ := routeBody["messages"].([]any)
	if len(msgs) == 0 || firstRole(msgs) != "system" {
		routeBody["messages"] = append([]any{map[string]any{"role": "system", "content": defaultWorkbuddySystemPrompt}}, msgs...)
	}

	routeBody["stream"] = true
	p.injectStreamUsage(routeBody, "/v1/chat/completions")

	return json.Marshal(routeBody)
}

// messagesOf reads the messages array a chat body carries.
func messagesOf(body map[string]any) []any {
	msgs, _ := body["messages"].([]any)

	return msgs
}

// firstRole returns the role of the first message, or "" when there is none.
func firstRole(msgs []any) string {
	if len(msgs) == 0 {
		return ""
	}
	if m, ok := msgs[0].(map[string]any); ok {
		role, _ := m["role"].(string)

		return role
	}

	return ""
}

// workbuddyThinkingEnabled resolves whether the outbound body turns DeepSeek
// reasoning on: an explicit thinking.type=enabled, or any effort other than
// none on a DeepSeek model — the same resolution build_upstream_body applies.
func workbuddyThinkingEnabled(body map[string]any, model string) bool {
	if len(model) < 8 || model[:8] != "deepseek" {
		return false
	}

	if thinking, ok := body["thinking"].(map[string]any); ok {
		if t, _ := thinking["type"].(string); t == "enabled" {
			return true
		}
		if t, _ := thinking["type"].(string); t == "disabled" {
			return false
		}
	}
	effort, _ := body["reasoning_effort"].(string)

	return effort != "none" && effort != ""
}
