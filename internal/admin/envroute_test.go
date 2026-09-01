package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const addRouteConfig = `# yaml-language-server: $schema=./schema.json
port: 1765

providers:
  - name: alpha # primary upstream
    api_key: ${ALPHA_KEY}
    base_url: https://alpha.example/v1
    style: openai

  - name: beta
    api_key: ${BETA_KEY}
    base_url: https://beta.example/v1
    style: openai

routes:
  - model_id: demo-model
    routes:
      - provider: alpha
        model: alpha-upstream
`

func TestAddRouteAppendsLegPreservingComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := NewEditor(path).AddRoute("demo-model", "beta", "beta-upstream", "", false); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"# yaml-language-server: $schema=./schema.json",
		"- name: alpha # primary upstream",
		"${ALPHA_KEY}",
		"provider: alpha",
		"        model: alpha-upstream",
		"      - provider: beta",
		"        model: beta-upstream",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q; body:\n%s", want, got)
		}
	}
}

func TestAddRouteWritesDefaultsAndDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	editor := NewEditor(path)
	if err := editor.AddRoute("demo-model", "beta", "beta-upstream", "high", false); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"defaults:",
		"reasoning_effort: high",
		"provider: alpha",
		"model: alpha-upstream",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q; body:\n%s", want, got)
		}
	}

	// A second leg without an effort must not inherit defaults, and a disabled
	// leg must be parked from birth.
	if err := editor.AddRoute("demo-model", "beta", "beta-again", "", true); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	got = string(body)
	if strings.Count(got, "defaults:") != 1 {
		t.Errorf("defaults count = %d, want 1:\n%s", strings.Count(got, "defaults:"), got)
	}
	if strings.Contains(got, "beta-again") && !strings.Contains(got, "disabled: true") {
		t.Errorf("disabled leg not written:\n%s", got)
	}
}

func TestAddRouteRejectsUnknownModelAndEmptyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor(path)

	if err := editor.AddRoute("ghost-model", "beta", "m", "", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown model error = %v, want not found", err)
	}
	if err := editor.AddRoute("demo-model", "", "m", "", false); err == nil {
		t.Error("empty provider accepted")
	}
	if err := editor.AddRoute("demo-model", "beta", "", "", false); err == nil {
		t.Error("empty model accepted")
	}
	if err := editor.AddRoute("demo-model", "beta", "m", "ultra", false); err == nil {
		t.Error("invalid reasoning effort accepted")
	}
}

func TestRemoveRouteDeletesLegAndKeepsFormatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor(path)
	if err := editor.AddRoute("demo-model", "beta", "beta-upstream", "high", false); err != nil {
		t.Fatal(err)
	}

	// Remove the original alpha leg — beta with defaults remains.
	if err := editor.RemoveRoute("demo-model", 0); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if strings.Contains(got, "alpha-upstream") {
		t.Errorf("removed leg still present:\n%s", got)
	}
	for _, want := range []string{
		"- name: alpha # primary upstream",
		"${ALPHA_KEY}",
		"provider: beta",
		"model: beta-upstream",
		"reasoning_effort: high",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml missing %q; body:\n%s", want, got)
		}
	}
}

func TestRemoveRouteGuards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(addRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor(path)

	if err := editor.RemoveRoute("demo-model", 0); err == nil || !strings.Contains(err.Error(), "last leg") {
		t.Errorf("single-leg removal error = %v, want last leg guard", err)
	}
	if err := editor.RemoveRoute("demo-model", 5); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("out-of-range removal error = %v, want out of range", err)
	}
	if err := editor.RemoveRoute("ghost-model", 0); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown model removal error = %v, want not found", err)
	}
}

func TestRouteAddEndpointValidatesAndPersists(t *testing.T) {
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

	w := request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"ghost","model":"m"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown provider status = %d, want 422", w.Code)
	}

	w = request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"beta","model":"beta-upstream"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", w.Code, w.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reloads = %d, want 1", *reloads)
	}

	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "model: beta-upstream") {
		t.Fatalf("yaml not updated:\n%s", updated)
	}

	var status struct {
		Models []struct {
			ModelID string `json:"model_id"`
			Chain   []struct {
				Provider string `json:"provider"`
			} `json:"chain"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Models) != 1 || len(status.Models[0].Chain) != 2 {
		t.Fatalf("status models = %+v, want 2 legs on demo-model", status.Models)
	}

	// Invalid reasoning_effort is rejected before the yaml is touched.
	w = request(t, srv, http.MethodPost, "/admin/api/routes/demo-model/add", session, `{"provider":"beta","model":"m","reasoning_effort":"ultra"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad effort status = %d, want 400", w.Code)
	}
}
