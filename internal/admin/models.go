package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"routerllm/internal/util"
)

func (d Deps) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	if d.Models == nil {
		writeError(w, http.StatusNotImplemented, "provider model catalog is unavailable")
		return
	}

	name := chi.URLParam(r, "name")

	configured := false
	for _, pc := range d.Registry().ProviderConfigs() {
		if pc.Name != name {
			continue
		}
		configured = true
		if pc.Disabled {
			writeError(w, http.StatusBadRequest, "provider "+name+" is disabled")
			return
		}
		break
	}
	if !configured {
		writeError(w, http.StatusNotFound, "provider "+name+" not found")
		return
	}
	if _, ok := d.Registry().Provider(name); !ok {
		writeError(w, http.StatusBadRequest, "provider "+name+" is not active")
		return
	}

	catalog, err := d.Models(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch model catalog")
		return
	}

	util.WriteJSON(w, http.StatusOK, catalog)
}
