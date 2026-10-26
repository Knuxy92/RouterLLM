package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseModelsPicksLongestAgentList(t *testing.T) {
	body := []byte(`{"data":{"agents":{"cli":{"models":["a","b"]},"craft":{"models":["a","b","c","d"]}}}}`)
	ids, err := ParseModels(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 4 || ids[0] != "a" || ids[3] != "d" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestParseModelsEmptyCatalog(t *testing.T) {
	ids, err := ParseModels([]byte(`{"data":{"agents":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil {
		t.Fatalf("ids = %v, want nil", ids)
	}
}

func TestFetchModelsUsesDesktopIdentity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(ConfigPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer wb-access" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-User-Id") != "uid-1" {
			t.Errorf("X-User-Id = %q", r.Header.Get("X-User-Id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"agents": map[string]any{"cli": map[string]any{"models": []string{"deepseek-v4.1-flash"}}},
		}})
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	t.Setenv("WORKBUDDY_INTL_CHAT_BASE", upstream.URL)
	ids, err := CatalogClient(0).FetchModels(context.Background(), "intl", "uid-1", "wb-access")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "deepseek-v4.1-flash" {
		t.Fatalf("ids = %v", ids)
	}
}
