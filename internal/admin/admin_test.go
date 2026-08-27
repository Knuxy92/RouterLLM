package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"routerllm/internal/config"
	"routerllm/internal/model"
	"routerllm/internal/provider"
)

func testDeps(t *testing.T, path string) (Deps, *int) {
	t.Helper()

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	reg := provider.NewRegistry(cfg.Providers, cfg.Routes, cfg.Cooldown)
	reloads := 0

	deps := Deps{
		Registry:  func() *provider.Registry { return reg },
		Editor:    NewEditor(path),
		Logs:      NewLogBuffer(),
		Reloads:   NewReloadTracker(),
		StartedAt: time.Now(),
	}
	deps.Reload = func() error {
		reloads++
		next, err := config.LoadFile(path)
		if err != nil {
			deps.Reloads.RecordFailure(err)
			return err
		}
		reg = provider.Rebuild(next.Providers, next.Routes, next.Cooldown, reg)
		deps.Reloads.RecordSuccess()

		return nil
	}

	return deps, &reloads
}

func adminServer(t *testing.T, deps Deps) http.Handler {
	t.Helper()

	r := chi.NewRouter()
	Mount(r, deps)

	return r
}

func seedConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	body := `port: 1765

providers:
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
    auth_mode: bearer

  - name: beta
    api_key: sk-beta
    base_url: https://beta.example
    style: openai
    auth_mode: bearer

routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
      - provider: beta
        model: upstream-b
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	return path
}

func request(t *testing.T, srv http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, target, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	return w
}

func TestAdminDisabledWithoutToken(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodGet, "/admin/api/status", "", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when token unset", w.Code)
	}
}

func TestAdminRejectsWrongToken(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	if w := request(t, srv, http.MethodGet, "/admin/api/status", "nope", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for wrong token", w.Code)
	}
	if w := request(t, srv, http.MethodGet, "/admin/api/status", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing token", w.Code)
	}
}

func TestStatusPayload(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodGet, "/admin/api/status", "secret", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var got Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ProvidersTotal != 2 || got.ProvidersActive != 2 {
		t.Fatalf("providers total/active = %d/%d, want 2/2", got.ProvidersTotal, got.ProvidersActive)
	}
	if len(got.Models) != 1 || len(got.Models[0].Chain) != 2 {
		t.Fatalf("models = %+v, want 1 model with a 2-leg chain", got.Models)
	}
	if !got.Models[0].Chain[0].Active || got.Models[0].Chain[1].Active {
		t.Fatalf("only the first leg should be active: %+v", got.Models[0].Chain)
	}
	if got.Providers[0].KeysTotal != 1 || got.Providers[0].KeysAlive != 1 {
		t.Fatalf("keys total/alive = %d/%d, want 1/1", got.Providers[0].KeysTotal, got.Providers[0].KeysAlive)
	}
	for _, p := range got.Providers {
		for _, k := range p.Keys {
			if strings.Contains(k.Masked, "sk-") {
				t.Fatalf("status leaked a raw key: %q", k.Masked)
			}
		}
	}
}

func TestProviderToggleWritesYAMLAndReloads(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, reloads := testDeps(t, path)
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodPost, "/admin/api/providers/beta", "secret", `{"disabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reloads = %d, want 1", *reloads)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "disabled: true") {
		t.Fatalf("yaml not updated:\n%s", data)
	}

	var got Status
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.ProvidersActive != 1 {
		t.Fatalf("providers active = %d, want 1 after disabling beta", got.ProvidersActive)
	}
}

func TestProviderToggleUnknownProvider(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodPost, "/admin/api/providers/ghost", "secret", `{"disabled":true}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
}

func TestRouteToggleAndMove(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, _ := testDeps(t, path)
	srv := adminServer(t, deps)

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/0", "secret", `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("toggle status = %d: %s", w.Code, w.Body.String())
	}

	var got Status
	w := request(t, srv, http.MethodGet, "/admin/api/status", "secret", "")
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Models[0].Chain[0].Disabled {
		t.Fatalf("first leg should be disabled: %+v", got.Models[0].Chain)
	}
	if !got.Models[0].Chain[1].Active {
		t.Fatalf("second leg should take over: %+v", got.Models[0].Chain)
	}

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/move", "secret", `{"index":1,"direction":"up"}`); w.Code != http.StatusOK {
		t.Fatalf("move status = %d: %s", w.Code, w.Body.String())
	}

	data, _ := os.ReadFile(path)
	if strings.Index(string(data), "upstream-b") > strings.Index(string(data), "upstream-a") {
		t.Fatalf("routes not reordered:\n%s", data)
	}
}

func TestRouteMoveValidatesDirection(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/move", "secret", `{"index":0,"direction":"sideways"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestReloadEndpointReportsRejection(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, _ := testDeps(t, path)
	srv := adminServer(t, deps)

	os.WriteFile(path, []byte("providers:\n  - name: broken\n    bad: [unclosed\n"), 0o600)

	w := request(t, srv, http.MethodPost, "/admin/api/reload", "secret", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for invalid config", w.Code)
	}
	if last := deps.Reloads.Last(); last.OK || last.Error == "" {
		t.Fatalf("last reload = %+v, want failure with an error", last)
	}
}

func TestLogsEndpointReturnsBufferedLines(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	deps.Logs.Write([]byte("first line\nsecond line\n"))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodGet, "/admin/api/logs", "secret", "")
	var payload struct {
		Entries []LogEntry `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &payload)
	if len(payload.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", payload.Entries)
	}

	w = request(t, srv, http.MethodGet, "/admin/api/logs?since=1", "secret", "")
	json.Unmarshal(w.Body.Bytes(), &payload)
	if len(payload.Entries) != 1 || payload.Entries[0].Line != "second line" {
		t.Fatalf("since filter broken: %+v", payload.Entries)
	}
}

func TestLogBufferCapsAtCapacity(t *testing.T) {
	buf := NewLogBuffer()
	for i := 0; i < defaultLogCapacity+50; i++ {
		buf.Write([]byte("line\n"))
	}

	if got := len(buf.Since(0)); got != defaultLogCapacity {
		t.Fatalf("buffered = %d, want %d", got, defaultLogCapacity)
	}
}

func TestStatusReflectsDisabledProviderFromConfig(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := filepath.Join(t.TempDir(), "routerllm.yaml")
	os.WriteFile(path, []byte(`providers:
  - name: parked
    disabled: true
    base_url: https://parked.example
    style: openai
  - name: alpha
    api_key: sk-alpha
    base_url: https://alpha.example
    style: openai
routes:
  - model_id: model-a
    routes:
      - provider: alpha
        model: upstream-a
`), 0o600)

	deps, _ := testDeps(t, path)
	srv := adminServer(t, deps)

	var got Status
	w := request(t, srv, http.MethodGet, "/admin/api/status", "secret", "")
	json.Unmarshal(w.Body.Bytes(), &got)

	if got.ProvidersTotal != 2 || got.ProvidersActive != 1 {
		t.Fatalf("total/active = %d/%d, want 2/1", got.ProvidersTotal, got.ProvidersActive)
	}

	var parked ProviderStatus
	for _, p := range got.Providers {
		if p.Name == "parked" {
			parked = p
		}
	}
	if !parked.Disabled || parked.Serving {
		t.Fatalf("parked provider = %+v, want disabled and not serving", parked)
	}
}

var _ = model.Rule{}
