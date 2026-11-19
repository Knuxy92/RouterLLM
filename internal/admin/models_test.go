package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"routerllm/internal/services"
)

func TestProviderModelsNotWired(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/providers/alpha/models", session, "")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", w.Code, w.Body.String())
	}
}

func TestProviderModelsUnknownProvider(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	deps.Models = func(context.Context, string) (services.ModelCatalog, error) {
		return services.ModelCatalog{}, nil
	}
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/providers/ghost/models", session, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestProviderModelsDisabledProvider(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")

	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	body := `providers:
  - name: parked
    disabled: true
    base_url: https://parked.example
    style: openai
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
    auth_mode: bearer
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deps, _ := testDeps(t, path)
	deps.Models = func(context.Context, string) (services.ModelCatalog, error) {
		return services.ModelCatalog{}, nil
	}
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/providers/parked/models", session, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestProviderModelsSuccess(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	want := services.ModelCatalog{
		Provider: "alpha",
		Style:    "openai",
		Source:   "live",
		Models:   []string{"model-one", "model-two"},
	}
	deps.Models = func(_ context.Context, name string) (services.ModelCatalog, error) {
		if name != "alpha" {
			t.Fatalf("provider = %q, want alpha", name)
		}

		return want, nil
	}
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/providers/alpha/models", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var got services.ModelCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Provider != want.Provider || got.Style != want.Style || got.Source != want.Source {
		t.Fatalf("catalog = %+v, want %+v", got, want)
	}
	if len(got.Models) != 2 || got.Models[0] != "model-one" || got.Models[1] != "model-two" {
		t.Fatalf("models = %v, want [model-one model-two]", got.Models)
	}
	for _, key := range []string{`"provider"`, `"style"`, `"source"`, `"models"`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("response missing %s: %s", key, w.Body.String())
		}
	}
}

func TestProviderModelsErrorDoesNotLeakUpstream(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	const secret = "UPSTREAM_SECRET_LEAK_9f2c"
	deps, _ := testDeps(t, seedConfig(t))
	deps.Models = func(context.Context, string) (services.ModelCatalog, error) {
		return services.ModelCatalog{}, errors.New(secret)
	}
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/providers/alpha/models", session, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("response leaked upstream detail: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to fetch model catalog") {
		t.Fatalf("response missing generic message: %s", w.Body.String())
	}
}
