package admin

import (
	"encoding/json"
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
	Logs      *LogBuffer
	Reloads   *ReloadTracker
	StartedAt time.Time
}

func Mount(r chi.Router, deps Deps) {
	if deps.Reload == nil || deps.Registry == nil || deps.Editor == nil || deps.Logs == nil || deps.Reloads == nil {
		panic("admin.Mount: Deps is missing a required field")
	}

	token := strings.TrimSpace(os.Getenv("ROUTERLLM_ADMIN_TOKEN"))

	r.Route("/admin/api", func(api chi.Router) {
		api.Use(requireToken(token))

		api.Get("/status", deps.handleStatus)
		api.Get("/logs", deps.handleLogs)
		api.Post("/reload", deps.handleReload)
		api.Post("/providers/{name}", deps.handleProviderToggle)
		api.Post("/routes/{model}/move", deps.handleRouteMove)
		api.Post("/routes/{model}/{index}", deps.handleRouteToggle)
	})
}

func requireToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				writeError(w, http.StatusForbidden, "admin API is disabled — set ROUTERLLM_ADMIN_TOKEN to enable it")
				return
			}

			supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if strings.TrimSpace(supplied) != token {
				writeError(w, http.StatusUnauthorized, "invalid admin token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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
