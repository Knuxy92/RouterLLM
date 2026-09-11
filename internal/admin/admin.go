package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"routerllm/internal/provider"
	"routerllm/internal/services"
	"routerllm/internal/telemetry"
)

type Deps struct {
	Registry  func() *provider.Registry
	Reload    func() error
	Editor    *Editor
	Sessions  *SessionStore
	Logs      *LogBuffer
	Reloads   *ReloadTracker
	Telemetry *telemetry.Store
	StartedAt time.Time
	// Test drives the provider test feature; nil disables the endpoint.
	Test func(r *http.Request, req services.TestRequest) *services.TestResult
}

func Mount(r chi.Router, deps Deps) {
	if deps.Reload == nil || deps.Registry == nil || deps.Editor == nil || deps.Sessions == nil || deps.Logs == nil || deps.Reloads == nil {
		panic("admin.Mount: Deps is missing a required field")
	}

	r.Route("/admin/api", func(api chi.Router) {
		api.Use(chimw.Compress(5))

		// The handshake itself runs without a session — it is how one is earned.
		api.Post("/auth/challenge", deps.handleAuthChallenge)
		api.Post("/auth/verify", deps.handleAuthVerify)

		api.Group(func(authed chi.Router) {
			authed.Use(deps.requireSession())

			authed.Post("/auth/logout", deps.handleAuthLogout)
			authed.Get("/pulse", deps.handlePulse)
			authed.Get("/status", deps.handleStatus)
			authed.Get("/logs", deps.handleLogs)
			authed.Get("/requests", deps.handleRequests)
			authed.Get("/metrics", deps.handleMetrics)
			authed.Post("/reload", deps.handleReload)
			authed.Post("/providers/{name}", deps.handleProviderToggle)
			authed.Post("/providers/{name}/test", deps.handleProviderTest)
			authed.Post("/providers/{name}/keys/{index}", deps.handleKeyToggle)
			authed.Post("/routes/{model}/move", deps.handleRouteMove)
			authed.Post("/routes/{model}/add", deps.handleRouteAdd)
			authed.Post("/routes/{model}/remove", deps.handleRouteRemove)
			authed.Post("/routes/{model}", deps.handleModelToggle)
			authed.Post("/routes/{model}/{index}", deps.handleRouteToggle)
		})
	})
}

// requireSession accepts only opaque session ids minted by the challenge–
// response handshake. The raw admin secret is never a valid credential on the
// wire.
func (d Deps) requireSession() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSpace(os.Getenv("ROUTERLLM_ADMIN_TOKEN")) == "" {
				writeError(w, http.StatusForbidden, "admin API is disabled — set ROUTERLLM_ADMIN_TOKEN to enable it")
				return
			}

			supplied := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !d.Sessions.Valid(supplied) {
				writeError(w, http.StatusUnauthorized, "invalid or expired admin session")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (d Deps) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(os.Getenv("ROUTERLLM_ADMIN_TOKEN")) == "" {
		writeError(w, http.StatusForbidden, "admin API is disabled — set ROUTERLLM_ADMIN_TOKEN to enable it")
		return
	}

	id, nonce, expiresIn := d.Sessions.Challenge()
	writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "nonce": nonce, "expires_in": expiresIn})
}

func (d Deps) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChallengeID string `json:"challenge_id"`
		Proof       string `json:"proof"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	session, expiresIn, ok := d.Sessions.Verify(body.ChallengeID, body.Proof)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or expired challenge proof")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"session": session, "expires_in": expiresIn})
}

func (d Deps) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	session := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	d.Sessions.Logout(session)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.buildStatus())
}

func (d Deps) handleLogs(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)

	writeJSON(w, http.StatusOK, map[string]any{"entries": d.Logs.Since(since)})
}

// handleRequests serves the request log two ways: ?since=<seq> returns the raw
// delta after that seq (used for cheap live tails), and the default page mode
// (?page=&per_page=&provider=&model=&level=&q=&hours=) returns one filtered,
// newest-first slice — the console only pulls the page it renders.
func (d Deps) handleRequests(w http.ResponseWriter, r *http.Request) {
	if d.Telemetry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []telemetry.Event{}, "latest": uint64(0)})
		return
	}

	q := r.URL.Query()
	if sinceStr := q.Get("since"); sinceStr != "" {
		since, _ := strconv.ParseUint(sinceStr, 10, 64)
		writeJSON(w, http.StatusOK, map[string]any{"entries": d.Telemetry.Since(since), "latest": d.Telemetry.Latest()})
		return
	}

	opts := telemetry.QueryOpts{
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
		Text:     q.Get("q"),
	}
	if levels := q.Get("level"); levels != "" {
		opts.Levels = map[string]bool{}
		for _, lv := range strings.Split(levels, ",") {
			if lv = strings.TrimSpace(lv); lv != "" {
				opts.Levels[lv] = true
			}
		}
	}
	if hours, err := strconv.ParseFloat(q.Get("hours"), 64); err == nil && hours > 0 {
		opts.NotBefore = time.Now().Add(-time.Duration(hours * float64(time.Hour)))
	}
	opts.Page, _ = strconv.Atoi(q.Get("page"))
	opts.PerPage, _ = strconv.Atoi(q.Get("per_page"))

	writeJSON(w, http.StatusOK, d.Telemetry.Query(opts))
}

func (d Deps) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if d.Telemetry == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}

	m := d.Telemetry.Metrics()
	reg := d.Registry()

	providers := make(map[string]telemetry.Summary)
	for _, pc := range reg.ProviderConfigs() {
		providers[pc.Name] = m.Summary("p:" + pc.Name)
	}

	legs := make(map[string]telemetry.Summary)
	models := make(map[string]telemetry.Summary)
	for _, rule := range reg.Rules() {
		models[rule.ModelID] = m.Summary("m:" + rule.ModelID)
		for _, spec := range rule.Routes {
			legs[spec.Provider+"/"+spec.Model] = m.Summary("l:" + spec.Provider + "/" + spec.Model)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"global":        m.Summary("g"),
		"global_weekly": m.SummarySince("g", time.Now().Add(-7*24*time.Hour)),
		"providers":     providers,
		"legs":          legs,
		"models":        models,
		"hourly":        m.Windows("g", time.Hour, 24),
		"weekly":        m.Windows("g", 24*time.Hour, 7),
	})
}

func (d Deps) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := d.Reload(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, d.buildStatus())
}

func (d Deps) handleProviderToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.Editor.SetProviderDisabled(chi.URLParam(r, "name"), body.Disabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

func (d Deps) handleRouteToggle(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "index must be an integer")
		return
	}

	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.Editor.SetRouteDisabled(chi.URLParam(r, "model"), index, body.Disabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

func (d Deps) handleModelToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.Editor.SetModelDisabled(chi.URLParam(r, "model"), body.Disabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

// handleKeyToggle flips a key's runtime-only manual disable. It deliberately
// does not touch the config file: keys arrive as ${ENV} placeholders, so there
// is nothing durable to write — the toggle lives in the key manager until the
// process restarts.
func (d Deps) handleKeyToggle(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "index must be an integer")
		return
	}

	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	live, ok := d.Registry().Provider(chi.URLParam(r, "name"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("provider %q is not active", chi.URLParam(r, "name")))
		return
	}
	if err := live.Keys.SetDisabledByIndex(index, body.Disabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, d.buildStatus())
}

func (d Deps) handleRouteMove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index     int    `json:"index"`
		Direction string `json:"direction"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Direction != "up" && body.Direction != "down" {
		writeError(w, http.StatusBadRequest, `direction must be "up" or "down"`)
		return
	}

	if err := d.Editor.MoveRoute(chi.URLParam(r, "model"), body.Index, body.Direction == "up"); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

func (d Deps) handleRouteAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		StyleCall       string `json:"stylecall"`
		Disabled        bool   `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch {
	case body.ReasoningEffort == "", validReasoningEffort[body.ReasoningEffort]:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("reasoning_effort must be one of %s or omitted", effortWhitelist()))
		return
	}
	switch {
	case body.StyleCall == "", validStyleCall[body.StyleCall]:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("stylecall must be one of %s or omitted", styleCallWhitelist()))
		return
	}

	if !d.providerConfigured(body.Provider) {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("provider %q is not configured", body.Provider))
		return
	}

	if err := d.Editor.AddRoute(chi.URLParam(r, "model"), body.Provider, body.Model, body.ReasoningEffort, body.StyleCall, body.Disabled); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

func (d Deps) handleRouteRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index int `json:"index"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.Editor.RemoveRoute(chi.URLParam(r, "model"), body.Index); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	d.applyNow(w)
}

func (d Deps) providerConfigured(name string) bool {
	for _, pc := range d.Registry().ProviderConfigs() {
		if pc.Name == name {
			return true
		}
	}

	return false
}

func (d Deps) applyNow(w http.ResponseWriter) {
	if err := d.Reload(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, d.buildStatus())
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()

	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "code": status}})
}
