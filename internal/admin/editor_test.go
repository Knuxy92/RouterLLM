package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `# RouterLLM configuration
port: 1765

providers:
  # primary gateway
  - name: opencode
    api_key: ${OPENCODE_API_KEY}   # multi-key via comma-split
    base_url: https://opencode.ai/zen
    style: openai
    auth_mode: bearer

  - name: crax
    api_key: ${CRAX_API_KEY}
    base_url: https://gpt.crax.lol/v1
    style: openai
    disabled: false

routes:
  # fallback chain, tried in order
  - model_id: opus-5
    routes:
      - provider: crax
        model: claude-opus-5
      - provider: opencode
        model: claude-opus-5-thinking
`

func newTestEditor(t *testing.T) (*Editor, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	return NewEditor(path), path
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func TestSetProviderDisabledPreservesEnvPlaceholders(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetProviderDisabled("opencode", true); err != nil {
		t.Fatalf("SetProviderDisabled() error = %v", err)
	}

	out := readFile(t, path)
	for _, placeholder := range []string{"${OPENCODE_API_KEY}", "${CRAX_API_KEY}"} {
		if !strings.Contains(out, placeholder) {
			t.Fatalf("placeholder %s missing after write — keys may have leaked:\n%s", placeholder, out)
		}
	}
	if strings.Contains(out, "sk-") {
		t.Fatalf("output contains a literal key value:\n%s", out)
	}
}

func TestSetProviderDisabledPreservesComments(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetProviderDisabled("crax", true); err != nil {
		t.Fatalf("SetProviderDisabled() error = %v", err)
	}

	out := readFile(t, path)
	for _, comment := range []string{"# RouterLLM configuration", "# primary gateway", "# fallback chain, tried in order", "# multi-key via comma-split"} {
		if !strings.Contains(out, comment) {
			t.Fatalf("comment %q lost after write:\n%s", comment, out)
		}
	}
}

func TestSetProviderDisabledInsertsAndUpdates(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetProviderDisabled("opencode", true); err != nil {
		t.Fatalf("insert error = %v", err)
	}
	if got := providerBlock(readFile(t, path), "opencode"); !strings.Contains(got, "disabled: true") {
		t.Fatalf("disabled not inserted for opencode:\n%s", got)
	}

	if err := editor.SetProviderDisabled("crax", true); err != nil {
		t.Fatalf("update error = %v", err)
	}
	if got := providerBlock(readFile(t, path), "crax"); !strings.Contains(got, "disabled: true") {
		t.Fatalf("existing disabled not updated for crax:\n%s", got)
	}

	if err := editor.SetProviderDisabled("crax", false); err != nil {
		t.Fatalf("revert error = %v", err)
	}
	if got := providerBlock(readFile(t, path), "crax"); !strings.Contains(got, "disabled: false") {
		t.Fatalf("disabled not reverted for crax:\n%s", got)
	}
}

func TestSetProviderDisabledUnknownProvider(t *testing.T) {
	editor, path := newTestEditor(t)
	before := readFile(t, path)

	err := editor.SetProviderDisabled("ghost", true)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want not found", err)
	}
	if readFile(t, path) != before {
		t.Fatal("file was modified despite the error")
	}
}

func TestSetRouteDisabled(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetRouteDisabled("opus-5", 1, true); err != nil {
		t.Fatalf("SetRouteDisabled() error = %v", err)
	}

	out := readFile(t, path)
	if !strings.Contains(out, "model: claude-opus-5-thinking") || !strings.Contains(out, "disabled: true") {
		t.Fatalf("route entry not disabled:\n%s", out)
	}
	if strings.Index(out, "disabled: true") < strings.Index(out, "claude-opus-5-thinking") {
		t.Fatalf("disabled applied to the wrong route entry:\n%s", out)
	}
}

func TestSetRouteDisabledIndexOutOfRange(t *testing.T) {
	editor, _ := newTestEditor(t)

	if err := editor.SetRouteDisabled("opus-5", 9, true); err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestMoveRouteSwapsOrder(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.MoveRoute("opus-5", 1, true); err != nil {
		t.Fatalf("MoveRoute() error = %v", err)
	}

	out := readFile(t, path)
	first := strings.Index(out, "claude-opus-5-thinking")
	second := strings.Index(out, "model: claude-opus-5\n")
	if first == -1 || second == -1 {
		t.Fatalf("both routes should still exist:\n%s", out)
	}
	if first > second {
		t.Fatalf("routes were not swapped:\n%s", out)
	}
}

func TestMoveRouteRejectsMoveOffEnds(t *testing.T) {
	editor, _ := newTestEditor(t)

	if err := editor.MoveRoute("opus-5", 0, true); err == nil {
		t.Fatal("expected error moving first route up")
	}
	if err := editor.MoveRoute("opus-5", 1, false); err == nil {
		t.Fatal("expected error moving last route down")
	}
}

func TestMutateWritesBackup(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetProviderDisabled("crax", true); err != nil {
		t.Fatalf("SetProviderDisabled() error = %v", err)
	}

	backup := readFile(t, path+".bak")
	if backup != sampleConfig {
		t.Fatalf("backup does not match the original config:\n%s", backup)
	}
}

func TestMutatedConfigStaysLoadable(t *testing.T) {
	editor, path := newTestEditor(t)

	if err := editor.SetProviderDisabled("opencode", true); err != nil {
		t.Fatalf("SetProviderDisabled() error = %v", err)
	}

	out := readFile(t, path)
	if !strings.Contains(out, "port: 1765") {
		t.Fatalf("top-level keys lost:\n%s", out)
	}
	if strings.Count(out, "model_id: opus-5") != 1 {
		t.Fatalf("routes section damaged:\n%s", out)
	}
}

func providerBlock(config, name string) string {
	start := strings.Index(config, "name: "+name)
	if start == -1 {
		return ""
	}

	rest := config[start:]
	if next := strings.Index(rest, "\n  - name:"); next != -1 {
		return rest[:next]
	}
	if end := strings.Index(rest, "\nroutes:"); end != -1 {
		return rest[:end]
	}

	return rest
}
