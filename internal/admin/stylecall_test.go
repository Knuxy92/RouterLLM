package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRouteWritesStyleCall(t *testing.T) {
	t.Setenv("ALPHA_KEY", "sk-alpha")
	t.Setenv("BETA_KEY", "sk-beta")
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := NewEditor(path).AddRoute("demo-model", "beta", "beta-upstream", "high", "responses", false); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	// Comments and ${ENV} placeholders survive the node edit.
	for _, want := range []string{
		"# yaml-language-server: $schema=./schema.json",
		"- name: alpha # primary upstream",
		"${ALPHA_KEY}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q; body:\n%s", want, got)
		}
	}

	// stylecall sits after model and before the defaults block.
	leg := "      - provider: beta\n        model: beta-upstream\n        stylecall: responses\n        defaults:\n          reasoning_effort: high\n"
	if !strings.Contains(got, leg) {
		t.Errorf("stylecall leg not written in order (after model, before defaults); body:\n%s", got)
	}
}

func TestAddRouteOmitsStyleCallWhenEmpty(t *testing.T) {
	t.Setenv("ALPHA_KEY", "sk-alpha")
	t.Setenv("BETA_KEY", "sk-beta")
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := NewEditor(path).AddRoute("demo-model", "beta", "beta-upstream", "", "", false); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if strings.Contains(got, "stylecall") {
		t.Errorf("empty stylecall must not be written; body:\n%s", got)
	}
}

func TestAddRouteRejectsBadStyleCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)

	err := NewEditor(path).AddRoute("demo-model", "beta", "m", "", "bogus", false)
	if err == nil {
		t.Fatal("bogus stylecall accepted")
	}
	const want = `invalid stylecall "bogus" (must be chat, responses, or messages)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if got := readFile(t, path); got != before {
		t.Errorf("file was modified despite the error:\n%s", got)
	}
}

func TestRouteAddEndpointStyleCall(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "token")
	t.Setenv("ALPHA_KEY", "sk-alpha")
	t.Setenv("BETA_KEY", "sk-beta")
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	deps, reloads := testDeps(t, path)
	srv := adminServer(t, deps)
	session := login(t, srv, "token")

	// Valid stylecall persists, bumps the reload counter, and shows up on the
	// leg in the returned status payload; the untouched first leg omits it.
	w := request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"beta","model":"beta-upstream","stylecall":"responses"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", w.Code, w.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reloads = %d, want 1", *reloads)
	}
	assertLegStyleCalls(t, w.Body.String(), []string{"", "responses"})

	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "stylecall: responses") {
		t.Fatalf("yaml not updated:\n%s", updated)
	}

	// Empty/absent stylecall keeps working and stays omitted in status.
	w = request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"beta","model":"beta-again"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add without stylecall status = %d body = %s", w.Code, w.Body.String())
	}
	if *reloads != 2 {
		t.Fatalf("reloads = %d, want 2", *reloads)
	}
	assertLegStyleCalls(t, w.Body.String(), []string{"", "responses", ""})

	// Invalid stylecall is rejected with a 400 before the yaml is touched.
	before := readFile(t, path)
	w = request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"beta","model":"m","stylecall":"bogus"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad stylecall status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "chat") || !strings.Contains(w.Body.String(), "responses") || !strings.Contains(w.Body.String(), "messages") {
		t.Errorf("400 body does not list allowed values: %s", w.Body.String())
	}
	if *reloads != 2 {
		t.Fatalf("reloads = %d, want 2 after rejected add", *reloads)
	}
	if got := readFile(t, path); got != before {
		t.Errorf("file was modified despite the 400:\n%s", got)
	}

	// The status endpoint reports the pinned leg too.
	w = request(t, srv, http.MethodGet, "/admin/api/status", session, "")
	assertLegStyleCalls(t, w.Body.String(), []string{"", "responses", ""})
}

func assertLegStyleCalls(t *testing.T, body string, want []string) {
	t.Helper()

	var status struct {
		Models []struct {
			ModelID string           `json:"model_id"`
			Chain   []map[string]any `json:"chain"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode status: %v; body: %s", err, body)
	}
	if len(status.Models) != 1 {
		t.Fatalf("models = %+v, want 1", status.Models)
	}
	chain := status.Models[0].Chain
	if len(chain) != len(want) {
		t.Fatalf("chain legs = %+v, want %d", chain, len(want))
	}
	for i, leg := range chain {
		got, present := leg["stylecall"].(string)
		if want[i] == "" {
			if present {
				t.Errorf("leg %d carries stylecall %q, want key omitted", i, got)
			}
			continue
		}
		if !present || got != want[i] {
			t.Errorf("leg %d stylecall = %q (present=%v), want %q", i, got, present, want[i])
		}
	}
}

func TestProviderTestEndpointStyleCall(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	captured := stubTest(t, &deps)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	if w := postTest(t, srv, session, `{"model":"upstream-a","stylecall":"bogus"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bogus stylecall status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := postTest(t, srv, session, `{"model":"upstream-a","stylecall":"messages"}`); w.Code != http.StatusOK {
		t.Fatalf("valid stylecall status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if captured.StyleCall != "messages" {
		t.Fatalf("captured stylecall = %q, want messages", captured.StyleCall)
	}
}
