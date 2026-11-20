package provider

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                                       "",
		"https://api.openai.com":                 "https://api.openai.com",
		"https://api.openai.com/v1":              "https://api.openai.com",
		"https://api.openai.com/v1/":             "https://api.openai.com",
		"https://agentrouter.org/v1":             "https://agentrouter.org",
		"https://api.kilo.ai/api/gateway":        "https://api.kilo.ai/api/gateway",
		"https://api.kilo.ai/api/gateway/v1":     "https://api.kilo.ai/api/gateway",
		"https://x.supabase.co/functions/v1/llm/v1": "https://x.supabase.co/functions/v1/llm",
		"https://generativelanguage.googleapis.com": "https://generativelanguage.googleapis.com",
		"https://api.cline.bot/api":              "https://api.cline.bot/api",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}