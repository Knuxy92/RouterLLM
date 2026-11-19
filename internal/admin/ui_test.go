package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMountUIUsesImmutableCacheForHashedAssets(t *testing.T) {
	r := chi.NewRouter()
	MountUI(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/_astro/does-not-exist.js", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("unknown asset fallback Cache-Control = %q, want no-store", got)
	}

	assetPath := firstHashedAsset(t)
	req = httptest.NewRequest(http.MethodGet, "/admin/"+assetPath, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q, want immutable cache", got)
	}
}

func TestMountUIServesPageDirectories(t *testing.T) {
	r := chi.NewRouter()
	MountUI(r)

	for _, page := range []string{"dashboard", "logs", "providers", "models"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/"+page, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /admin/%s status = %d, want 200", page, w.Code)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET /admin/%s Cache-Control = %q, want no-store", page, got)
		}
	}
}

func firstHashedAsset(t *testing.T) string {
	t.Helper()

	for _, dir := range []string{"dist/_astro", "dist/assets"} {
		entries, err := fs.ReadDir(assets, dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "-") {
				prefix := strings.TrimPrefix(dir, "dist/")
				return prefix + "/" + entry.Name()
			}
		}
	}

	t.Fatal("no hashed embedded asset found")
	return ""
}
