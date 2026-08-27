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

	req := httptest.NewRequest(http.MethodGet, "/admin/assets/does-not-exist.js", nil)
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

func firstHashedAsset(t *testing.T) string {
	t.Helper()

	entries, err := fs.ReadDir(assets, "dist/assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "-") {
			return "assets/" + entry.Name()
		}
	}

	t.Fatal("no hashed embedded asset found")
	return ""
}
