package workbuddy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Fingerprint sanitization, ported from the reference gateway's wb_proxy
// sanitize stage: it immunizes request payloads against the WorkBuddy
// upstream WAF, which rejects known Codex / Claude Code / DeepSeek client
// fingerprints (code 11-128 / 11128) with otherwise-valid requests.

var sanitizeFeatures = []string{
	"x-anthropic-billing-header",
	"cc_entrypoint=",
	"You are Claude Code",
	"Main branch (",
	"You are a coding agent running in the Codex CLI",
	"github.com/anthropics/",
	"11128",
}

var sanitizeRewrites = [][2]string{
	{"You are Claude Code, Anthropic's official CLI for Claude", "You are Claude Code, Anthropic's official CLI tool for Claude"},
	{"Main branch (you will usually use this for PRs)", "Default branch (you will usually use this for PRs)"},
	{"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.", "You are a coding agent running in the Codex CLI tool, a terminal-based coding assistant."},
	{"To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues", "To provide feedback, users should report the issue at https://github.com/anthropics/claude-code/issues"},
	{"11128", "11-128"},
}

var (
	sanitizeHDRRE     = regexp.MustCompile(`(?i)x-anthropic-billing-header:[^;\r\n]*;?\s*`)
	sanitizeBareHDRRE = regexp.MustCompile(`(?i)x-anthropic-billing-header`)
	sanitizeKVRE      = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\r\n]*;?\s*`)

	// Only this exact OmO fingerprint is rewritten; either token alone is
	// accepted by the upstream, so the rewrite stays deliberately narrow.
	sanitizeOmoJuniorRE = regexp.MustCompile(`(?i)Sisyphus-Junior - Focused executor from OhMyOpenCode`)

	// Head of the "You are ... <verb> ..." self-introduction sentence. The
	// Python reference matches the sentence tail with lookaheads, which Go's
	// RE2 lacks — here the head regex stops after the verb phrase and
	// replaceIdentitySentences extends to the sentence boundary in code.
	identityHeadRE = regexp.MustCompile(`(?i)You are\s+(?:an?\s+|the\s+)?(?:[\p{L}\p{N}_.-]+\s+){0,3}?(?:agent|assistant|model|AI|LLM)?[\s,]*(?:powered by|built on|built by|created by|developed by|made by|trained by)`)

	// GUI description paragraph: minimal word-order change at the end of the
	// first sentence breaks the "A sentence + B sentence" contiguous match.
	sanitizeDSHGUIRE = regexp.MustCompile(`(?i)(You are interacting with the user through the DeepSeek[ \t]+Harness[ \t]+Web[ \t]+GUI) whose URL the runtime context names\.`)

	// Prose brand reduction: only the space/tab form; deepseek-harness style
	// paths and identifiers stay untouched.
	sanitizeBrandRE = regexp.MustCompile(`(?i)DeepSeek[ \t]+Harness`)
)

const (
	sanitizeIdentityReplacement = "You are a helpful AI assistant."
	sanitizeDSHGUIReplacement   = "${1} whose URL is named in the runtime context."
	sanitizeBareHDRReplacement  = "x-anthropic-billing-hdr"

	// Lowercase literal probes backing the regex prefilters in HasFingerprint.
	sanitizeBareHDRLower   = "x-anthropic-billing-header"
	sanitizeOmoJuniorLower = "sisyphus-junior - focused executor from ohmyopencode"
	sanitizeDSHGUILower    = "whose url the runtime context names"
)

var sanitizeIdentityVerbs = []string{
	"powered by", "built on", "built by", "created by",
	"developed by", "made by", "trained by",
}

// NormalizeRoles maps role names the upstream rejects onto ones it accepts.
// WorkBuddy only knows system / user / assistant / tool; OpenAI's newer
// "developer" role is the same thing as "system", but sending it verbatim
// fails with code 11128.
func NormalizeRoles(messages []any) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		item, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)

			continue
		}

		if role, _ := item["role"].(string); role == "developer" {
			renamed := cloneMap(item)
			renamed["role"] = "system"
			out = append(out, renamed)

			continue
		}
		out = append(out, m)
	}

	return out
}

// cloneMap shallow-copies a decoded message map so sanitizers never mutate
// the caller's request in place.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

// HasFingerprint reports whether text carries a known upstream WAF
// fingerprint. identity selects the self-introduction family (brand, GUI
// paragraph, "You are ... <verb>" sentences); the literal features and the
// billing-header checks run for every role.
func HasFingerprint(text string, identity bool) bool {
	if text == "" {
		return false
	}
	for _, f := range sanitizeFeatures {
		if strings.Contains(text, f) {
			return true
		}
	}

	low := strings.ToLower(text)
	if strings.Contains(low, sanitizeBareHDRLower) || strings.Contains(low, sanitizeOmoJuniorLower) {
		return true
	}
	if !identity {
		return false
	}

	// `harness` is the cheap pre-filter; the regex decides.
	if strings.Contains(low, "harness") && sanitizeBrandRE.MatchString(text) {
		return true
	}
	if strings.Contains(low, sanitizeDSHGUILower) && sanitizeDSHGUIRE.MatchString(text) {
		return true
	}
	// The verb phrases are only a cheap pre-filter too: "powered by" in an
	// unrelated sentence (a tool's product description) neither enters the
	// rest of the rewrite nor gets rewritten itself.
	if !containsAny(low, sanitizeIdentityVerbs) {
		return false
	}

	return identityHeadRE.MatchString(text)
}

func containsAny(text string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}

	return false
}

// identitySentenceEnd extends a verb-phrase match to the end of its
// sentence, replacing the Python regex's lookahead tail: consume up to and
// including the first '.' followed by whitespace or end of text, but stop
// before the first newline when no such '.' comes first.
func identitySentenceEnd(text string, from int) int {
	for i := from; i < len(text); i++ {
		switch text[i] {
		case '\n':
			return i
		case '.':
			if i+1 >= len(text) {
				return i + 1
			}
			if r, _ := utf8.DecodeRuneInString(text[i+1:]); unicode.IsSpace(r) {
				return i + 1
			}
		}
	}

	return len(text)
}

// replaceIdentitySentences rewrites every "You are ... <verb> ..." sentence
// to the neutral replacement, one non-overlapping match at a time.
func replaceIdentitySentences(text string) string {
	var b strings.Builder
	last := 0
	for {
		loc := identityHeadRE.FindStringIndex(text[last:])
		if loc == nil {
			break
		}

		start := last + loc[0]
		end := identitySentenceEnd(text, last+loc[1])
		b.WriteString(text[last:start])
		b.WriteString(sanitizeIdentityReplacement)
		last = end
	}
	if last == 0 {
		return text
	}

	b.WriteString(text[last:])

	return b.String()
}

// SanitizeText neutralizes known fingerprints in one text blob. identity
// selects the self-introduction rewrites; tool arguments and reasoning
// traces are sanitized with identity=false — they are application data, not
// a client fingerprint.
func SanitizeText(text string, identity bool) string {
	if text == "" {
		return text
	}
	if !HasFingerprint(text, identity) {
		return text
	}

	text = sanitizeOmoJuniorRE.ReplaceAllString(text, "Sisyphus-Junior - Focused executor")
	if identity {
		text = replaceIdentitySentences(text)
		text = sanitizeDSHGUIRE.ReplaceAllString(text, sanitizeDSHGUIReplacement)

		// Brand reduction runs last: the GUI rule still matches the full name.
		text = sanitizeBrandRE.ReplaceAllString(text, "DeepSeek")
	}
	for _, rw := range sanitizeRewrites {
		text = strings.ReplaceAll(text, rw[0], rw[1])
	}
	text = sanitizeHDRRE.ReplaceAllString(text, "")
	if strings.Contains(text, "cc_") {
		prev := ""
		for prev != text {
			prev = text
			text = sanitizeKVRE.ReplaceAllString(text, "")
		}
	}

	return strings.TrimSpace(sanitizeBareHDRRE.ReplaceAllString(text, sanitizeBareHDRReplacement))
}

// SanitizeContent sanitizes a message content field: either a plain string
// or an array of typed parts, of which only "text" parts are rewritten.
func SanitizeContent(content any, identity bool) any {
	if s, ok := content.(string); ok {
		return SanitizeText(s, identity)
	}

	parts, ok := content.([]any)
	if !ok {
		return content
	}

	out := make([]any, 0, len(parts))
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			out = append(out, part)

			continue
		}
		if t, _ := p["type"].(string); t != "text" {
			out = append(out, part)

			continue
		}
		if _, present := p["text"]; !present {
			out = append(out, part)

			continue
		}

		copied := cloneMap(p)
		if s, ok := copied["text"].(string); ok {
			copied["text"] = SanitizeText(s, identity)
		}
		out = append(out, copied)
	}

	return out
}

// SanitizeToolCalls normalizes tool_calls entries: function.arguments must
// end up a JSON string — the upstream rejects any other shape with code
// 11101 — and string arguments go through the non-identity sanitize.
func SanitizeToolCalls(toolCalls any) any {
	calls, ok := toolCalls.([]any)
	if !ok {
		return toolCalls
	}

	out := make([]any, 0, len(calls))
	for _, tc := range calls {
		item, ok := tc.(map[string]any)
		if !ok {
			out = append(out, tc)

			continue
		}

		copied := cloneMap(item)
		if fn, ok := copied["function"].(map[string]any); ok {
			copied["function"] = sanitizeToolFunction(fn)
		}
		out = append(out, copied)
	}

	return out
}

func sanitizeToolFunction(fn map[string]any) map[string]any {
	copied := cloneMap(fn)
	// A missing arguments key counts as nil, same as the reference.
	switch v := copied["arguments"].(type) {
	case string:
		copied["arguments"] = SanitizeText(v, false)
	case nil:
		copied["arguments"] = "{}"
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			copied["arguments"] = fmt.Sprint(v)

			break
		}
		copied["arguments"] = string(encoded)
	}

	return copied
}

// SanitizeMessages walks a chat completion message array. Identity rewrites
// apply to system / developer / assistant roles only — user and tool content
// is user data and keeps its identity text; reasoning traces and tool
// arguments are application data and are sanitized with identity=false.
func SanitizeMessages(messages []any) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		item, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)

			continue
		}

		copied := cloneMap(item)
		role, _ := copied["role"].(string)
		identity := role == "system" || role == "developer" || role == "assistant"
		if content, present := copied["content"]; present {
			copied["content"] = SanitizeContent(content, identity)
		}
		if rc, ok := copied["reasoning_content"].(string); ok {
			copied["reasoning_content"] = SanitizeText(rc, false)
		}
		if tc, present := copied["tool_calls"]; present {
			copied["tool_calls"] = SanitizeToolCalls(tc)
		}
		out = append(out, copied)
	}

	return out
}

// BackfillReasoningContent adds an empty reasoning_content to every assistant
// message of a DeepSeek thinking request that lacks one: the upstream
// requires the field once thinking is enabled, and an empty string passes its
// presence check while carrying no model-visible semantics.
func BackfillReasoningContent(messages []any, model string, thinkingEnabled bool) []any {
	if !thinkingEnabled || !strings.HasPrefix(strings.ToLower(model), "deepseek") {
		return messages
	}

	out := make([]any, 0, len(messages))
	for _, m := range messages {
		item, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)

			continue
		}
		if role, _ := item["role"].(string); role != "assistant" {
			out = append(out, m)

			continue
		}

		copied := cloneMap(item)
		if _, ok := copied["reasoning_content"].(string); !ok {
			copied["reasoning_content"] = ""
		}
		out = append(out, copied)
	}

	return out
}
