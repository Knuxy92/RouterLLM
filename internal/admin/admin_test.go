package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
		Sessions:  NewSessionStore(func() string { return os.Getenv("ROUTERLLM_ADMIN_TOKEN") }),
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

// login performs the challenge–response handshake and returns a live session id.
func login(t *testing.T, srv http.Handler, secret string) string {
	t.Helper()

	w := request(t, srv, http.MethodPost, "/admin/api/auth/challenge", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("challenge status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var ch struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ch.Nonce))
	proof := hex.EncodeToString(mac.Sum(nil))

	session, ok := verifySession(t, srv, ch.ChallengeID, proof)
	if !ok {
		t.Fatal("login failed: verify rejected a correct proof")
	}

	return session
}

func verifySession(t *testing.T, srv http.Handler, challengeID, proof string) (string, bool) {
	t.Helper()

	w := request(t, srv, http.MethodPost, "/admin/api/auth/verify", "",
		fmt.Sprintf(`{"challenge_id":%q,"proof":%q}`, challengeID, proof))
	if w.Code != http.StatusOK {
		return "", false
	}

	var v struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}

	return v.Session, v.Session != ""
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

	w := request(t, srv, http.MethodPost, "/admin/api/auth/challenge", "", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("challenge = %d, want 403 when token unset", w.Code)
	}
	if w := request(t, srv, http.MethodGet, "/admin/api/status", "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when token unset", w.Code)
	}
}

func TestAuthRejectsRawSecretOnTheWire(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	// The secret itself is no longer a credential: sending it as a bearer
	// token must fail even though it is the correct secret.
	if w := request(t, srv, http.MethodGet, "/admin/api/status", "secret", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for raw secret", w.Code)
	}
	if w := request(t, srv, http.MethodGet, "/admin/api/status", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for missing session", w.Code)
	}

	session := login(t, srv, "secret")
	if w := request(t, srv, http.MethodGet, "/admin/api/status", session, ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with session: %s", w.Code, w.Body.String())
	}
}

func TestAuthRejectsWrongProofAndReplay(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodPost, "/admin/api/auth/challenge", "", "")
	var ch struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
	}
	json.Unmarshal(w.Body.Bytes(), &ch)

	// Wrong secret -> wrong proof, and the challenge is burned either way.
	if _, ok := verifySession(t, srv, ch.ChallengeID, "deadbeef"); ok {
		t.Fatal("verify accepted a wrong proof")
	}
	correct := hmac.New(sha256.New, []byte("secret"))
	correct.Write([]byte(ch.Nonce))
	if _, ok := verifySession(t, srv, ch.ChallengeID, hex.EncodeToString(correct.Sum(nil))); ok {
		t.Fatal("verify allowed replaying a burned challenge")
	}

	// A fresh challenge with a proof from the wrong secret is rejected too.
	w = request(t, srv, http.MethodPost, "/admin/api/auth/challenge", "", "")
	json.Unmarshal(w.Body.Bytes(), &ch)
	wrong := hmac.New(sha256.New, []byte("not-the-secret"))
	wrong.Write([]byte(ch.Nonce))
	if _, ok := verifySession(t, srv, ch.ChallengeID, hex.EncodeToString(wrong.Sum(nil))); ok {
		t.Fatal("verify accepted proof from wrong secret")
	}
}

func TestAuthRejectsExpiredChallenge(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	w := request(t, srv, http.MethodPost, "/admin/api/auth/challenge", "", "")
	var ch struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
	}
	json.Unmarshal(w.Body.Bytes(), &ch)

	deps.Sessions.mu.Lock()
	expired := deps.Sessions.challenges[ch.ChallengeID]
	expired.expiry = time.Now().Add(-time.Second)
	deps.Sessions.challenges[ch.ChallengeID] = expired
	deps.Sessions.mu.Unlock()

	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(ch.Nonce))
	if _, ok := verifySession(t, srv, ch.ChallengeID, hex.EncodeToString(mac.Sum(nil))); ok {
		t.Fatal("verify accepted an expired challenge")
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)

	session := login(t, srv, "secret")
	if !deps.Sessions.Valid(session) {
		t.Fatal("fresh session should be valid")
	}

	deps.Sessions.mu.Lock()
	deps.Sessions.sessions[session] = time.Now().Add(-time.Second)
	deps.Sessions.mu.Unlock()

	if deps.Sessions.Valid(session) {
		t.Fatal("expired session should be invalid")
	}
	if w := request(t, srv, http.MethodGet, "/admin/api/status", session, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for expired session", w.Code)
	}
}

func TestSessionStoreCapsAtMaxSessions(t *testing.T) {
	store := NewSessionStore(func() string { return "secret" })
	for i := 0; i < maxSessions; i++ {
		if _, _, ok := store.Verify(mustChallenge(t, store), proofOf(t, store, "secret")); !ok {
			t.Fatal("verify failed for correct proof")
		}
	}
	if len(store.sessions) != maxSessions {
		t.Fatalf("sessions = %d, want %d", len(store.sessions), maxSessions)
	}

	// One more mints a session and evicts the oldest — never exceeds the cap.
	if _, _, ok := store.Verify(mustChallenge(t, store), proofOf(t, store, "secret")); !ok {
		t.Fatal("verify failed when store is full")
	}
	if len(store.sessions) != maxSessions {
		t.Fatalf("sessions = %d, want cap %d enforced", len(store.sessions), maxSessions)
	}
}

func mustChallenge(t *testing.T, store *SessionStore) string {
	t.Helper()

	id, _, _ := store.Challenge()

	return id
}

func proofOf(t *testing.T, store *SessionStore, secret string) string {
	t.Helper()

	store.mu.Lock()
	var nonce string
	for _, ch := range store.challenges {
		nonce = ch.nonce
	}
	store.mu.Unlock()

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce))

	return hex.EncodeToString(mac.Sum(nil))
}

func TestStatusPayload(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/status", session, "")
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
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodPost, "/admin/api/providers/beta", session, `{"disabled":true}`)
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
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodPost, "/admin/api/providers/ghost", session, `{"disabled":true}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
}

func TestRouteToggleAndMove(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, _ := testDeps(t, path)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/0", session, `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("toggle status = %d: %s", w.Code, w.Body.String())
	}

	var got Status
	w := request(t, srv, http.MethodGet, "/admin/api/status", session, "")
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Models[0].Chain[0].Disabled {
		t.Fatalf("first leg should be disabled: %+v", got.Models[0].Chain)
	}
	if !got.Models[0].Chain[1].Active {
		t.Fatalf("second leg should take over: %+v", got.Models[0].Chain)
	}

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/move", session, `{"index":1,"direction":"up"}`); w.Code != http.StatusOK {
		t.Fatalf("move status = %d: %s", w.Code, w.Body.String())
	}

	data, _ := os.ReadFile(path)
	if strings.Index(string(data), "upstream-b") > strings.Index(string(data), "upstream-a") {
		t.Fatalf("routes not reordered:\n%s", data)
	}
}

func TestRouteRemoveEndpoint(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, reloads := testDeps(t, path)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/remove", session, `{"index":0}`); w.Code != http.StatusOK {
		t.Fatalf("remove status = %d: %s", w.Code, w.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reloads = %d, want 1", *reloads)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "upstream-a") {
		t.Fatalf("removed leg still in yaml:\n%s", data)
	}

	// Removing the now-last leg is rejected.
	w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/remove", session, `{"index":0}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("last-leg remove status = %d, want 422: %s", w.Code, w.Body.String())
	}
}

func TestRouteMoveValidatesDirection(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	deps, _ := testDeps(t, seedConfig(t))
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	if w := request(t, srv, http.MethodPost, "/admin/api/routes/model-a/move", session, `{"index":0,"direction":"sideways"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestReloadEndpointReportsRejection(t *testing.T) {
	t.Setenv("ROUTERLLM_ADMIN_TOKEN", "secret")
	path := seedConfig(t)
	deps, _ := testDeps(t, path)
	srv := adminServer(t, deps)
	session := login(t, srv, "secret")

	os.WriteFile(path, []byte("providers:\n  - name: broken\n    bad: [unclosed\n"), 0o600)

	w := request(t, srv, http.MethodPost, "/admin/api/reload", session, "")
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
	session := login(t, srv, "secret")

	w := request(t, srv, http.MethodGet, "/admin/api/logs", session, "")
	var payload struct {
		Entries []LogEntry `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &payload)
	if len(payload.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", payload.Entries)
	}

	w = request(t, srv, http.MethodGet, "/admin/api/logs?since=1", session, "")
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
	session := login(t, srv, "secret")

	var got Status
	w := request(t, srv, http.MethodGet, "/admin/api/status", session, "")
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
