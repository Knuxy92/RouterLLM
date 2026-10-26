// Package workbuddy holds WorkBuddy account auth, the outbound identity
// header family, and the upstream fingerprint sanitize for style: workbuddy
// providers. The upstream speaks OpenAI chat shape on /v2/chat/completions;
// the account pool keys are refresh tokens exchanged for short-lived access
// tokens at request time, mirroring internal/opencode.
//
// Header values and endpoints are ported from the reference gateway
// (workbuddy2api-hub, MIT) and its wb_identity module, which verified them
// against desktop-client captures (5.8.4 intl / 5.8.3 cn).
package workbuddy

import "strings"

const (
	// DesktopVersionIntl is the intl desktop client version verified by
	// capture (2026-10-16). The UA's trailing CLI segment is the client's
	// bundled CLI, not the desktop version itself.
	DesktopVersionIntl = "5.8.4"
	DesktopUAIntl      = "WorkBuddy/5.8.4 workbuddy-ai/5.8.4 CLI/2.161.0"
	DesktopVersionCN   = "5.8.3"
	DesktopUACN        = "WorkBuddy/5.8.3 WorkBuddy/5.8.3 CLI/2.161.0"

	// ChatPath is the upstream chat endpoint under the realm host.
	ChatPath = "/v2/chat/completions"
	// ConfigPath is the upstream product-config endpoint (model catalog).
	ConfigPath = "/v3/config"
	// RefreshPath is the account token refresh endpoint.
	RefreshPath = "/v2/plugin/auth/token/refresh"
	// AuthStatePath and AuthTokenPath back the browser OAuth login.
	AuthStatePath = "/v2/plugin/auth/state"
	AuthTokenPath = "/v2/plugin/auth/token"

	// DefaultMachineSalt seeds the per-account machine/session ids; the salt
	// is fixed so the same account always presents the same identity.
	DefaultMachineSalt = "workbuddy-routerllm"
)

// RealmHosts returns the chat base URL, origin and domain for a realm.
type RealmHosts struct {
	ChatBase string
	Origin   string
	Domain   string
}

// Hosts resolves the realm's endpoints. Anything but "cn" is the intl realm.
func Hosts(realm string) RealmHosts {
	if realm == "cn" {
		return RealmHosts{
			ChatBase: "https://copilot.tencent.com",
			Origin:   "https://copilot.tencent.com",
			Domain:   "copilot.tencent.com",
		}
	}

	return RealmHosts{
		ChatBase: "https://www.workbuddy.ai",
		Origin:   "https://www.workbuddy.ai",
		Domain:   "www.workbuddy.ai",
	}
}

// NormalizeRealm maps the accepted spellings onto intl/cn.
func NormalizeRealm(realm string) string {
	switch strings.ToLower(strings.TrimSpace(realm)) {
	case "cn", "china", "tencent":
		return "cn"
	default:
		return "intl"
	}
}

// NormalizeProduct maps the accepted spellings onto desktop/vscode/cli.
func NormalizeProduct(product string) string {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "vscode", "vsc", "ide":
		return "vscode"
	case "cli", "codebuddy":
		return "cli"
	default:
		return "desktop"
	}
}

// DesktopUA returns the identity UA for a realm.
func DesktopUA(realm string) string {
	if NormalizeRealm(realm) == "cn" {
		return DesktopUACN
	}

	return DesktopUAIntl
}

// DesktopVersion returns the identity version for a realm.
func DesktopVersion(realm string) string {
	if NormalizeRealm(realm) == "cn" {
		return DesktopVersionCN
	}

	return DesktopVersionIntl
}
