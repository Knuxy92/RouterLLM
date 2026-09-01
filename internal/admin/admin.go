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

	"routerllm/internal/provider"
)

type Deps struct {
	Registry  func() *provider.Registry
	Reload    func() error
	Editor    *Editor
	Sessions  *SessionStore
	Logs      *LogBuffer
	Reloads   *ReloadTracker
	StartedAt time.Time
}

func Mount(r chi.Router, deps Deps) {
	if deps.Reload == nil || deps.Registry == nil || deps.Editor == nil || deps.Sessions == nil || deps.Logs == nil || deps.Reloads == nil {
		panic("admin.Mount: Deps is missing a required field")
	}

	r.Route("/admin/api", func(api chi.Router) {
		// The handshake itself runs without a session — it is how one is earned.
		api.Post("/auth/challenge", deps.handleAuthChallenge)
		api.Post("/auth/verify", deps.handleAuthVerify)

		api.Group(func(authed chi.Router) {
			authed.Use(deps.requireSession())

			authed.Get("/status", deps.handleStatus)
			authed.Get("/logs", deps.handleLogs)
			authed.Post("/reload", deps.handleReload)
			authed.Post("/providers/{name}", deps.handleProviderToggle)
			authed.Post("/routes/{model}/move", deps.handleRouteMove)
			authed.Post("/routes/{model}/add", deps.handleRouteAdd)
			authed.Post("/routes/{model}/remove", deps.handleRouteRemove)
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

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.buildStatus())
}

func (d Deps) handleLogs(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)

	writeJSON(w, http.StatusOK, map[string]any{"entries": d.Logs.Since(since)})
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
		Disabled        bool   `json:"disabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch body.ReasoningEffort {
	case "", "low", "medium", "high", "max":
	default:
		writeError(w, http.StatusBadRequest, `reasoning_effort must be one of "low", "medium", "high", "max" or omitted`)
		return
	}

	if !d.providerConfigured(body.Provider) {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("provider %q is not configured", body.Provider))
		return
	}

	if err := d.Editor.AddRoute(chi.URLParam(r, "model"), body.Provider, body.Model, body.ReasoningEffort, body.Disabled); err != nil {
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
