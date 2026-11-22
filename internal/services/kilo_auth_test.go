package services

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiloOrgLogsStoreMissOnce(t *testing.T) {
	writeKiloAccounts(t, "tok_other") // tok_live is not in the store

	var buf bytes.Buffer
	proxy := NewProxy(kiloRegistry("http://unused", "tok_live"), http.DefaultClient, log.New(&buf, "", 0), false, false, false, true, nil, "")

	for i := 0; i < 3; i++ {
		if org := proxy.kiloOrg("tok_live"); org != "" {
			t.Fatalf("org = %q, want empty for an unknown key", org)
		}
	}

	if n := strings.Count(buf.String(), "not in the accounts store"); n != 1 {
		t.Fatalf("store-miss warning logged %d times, want 1:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "kilo key") || !strings.Contains(buf.String(), "X-KILOCODE-ORGANIZATIONID") {
		t.Fatalf("warning lacks the masked key context:\n%s", buf.String())
	}
}

// An account without an organization legitimately omits the header — silent.
func TestKiloOrgSilentForOrglessAccount(t *testing.T) {
	accounts := filepath.Join(t.TempDir(), "kilo-accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"accounts":[{"accountId":"acc_1","token":"tok_live","createdAt":"2026-01-01T00:00:00Z"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_ACCOUNTS_FILE", accounts)

	var buf bytes.Buffer
	proxy := NewProxy(kiloRegistry("http://unused", "tok_live"), http.DefaultClient, log.New(&buf, "", 0), false, false, false, true, nil, "")

	if org := proxy.kiloOrg("tok_live"); org != "" {
		t.Fatalf("org = %q, want empty for an org-less account", org)
	}
	if buf.Len() != 0 {
		t.Fatalf("org-less account logged unexpectedly:\n%s", buf.String())
	}
}

// A dead kilo key is a stale credential, not a transient failure: the log
// must tell the operator how to recover, and the key leaves the pool at once.
func TestForwardKiloLogsStaleTokenHint(t *testing.T) {
	writeKiloAccounts(t, "tok_live")

	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid token"}}`)
	}))
	defer upstream.Close()

	var buf bytes.Buffer
	proxy := NewProxy(kiloRegistry(upstream.URL, "tok_live"), upstream.Client(), log.New(&buf, "", 0), false, false, false, true, nil, "")
	resp, _, _, err := proxy.ForwardRaw("/v1/chat/completions", httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("")), map[string]any{
		"model":    "kilo/auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal("forward unexpectedly succeeded on a 401")
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	if attempts != 1 {
		t.Fatalf("upstream attempts = %d, want 1 (401 must not retry)", attempts)
	}
	if !strings.Contains(buf.String(), "routerllm --kilo-login") {
		t.Fatalf("stale-token hint missing from log:\n%s", buf.String())
	}
}
