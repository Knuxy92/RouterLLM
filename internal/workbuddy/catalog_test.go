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

func TestParseModelsLiveSchema(t *testing.T) {
	body := []byte(`{"code":0,"data":{"agents":[{"name":"cli","models":["default-model","hy4flash","deepseek-v4.1-flash-sg"]}],"models":[{"id":"default-model","name":"Auto"},{"id":"hy4flash","name":"Hy4 flash"},{"id":"deepseek-v4.1-flash-sg","name":"DeepSeek"},{"id":"hy4flash-free","name":"Hy4 flash"},{"id":"kling-video-v3","name":"Kling"}]}}`)
	ids, err := ParseModels(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"default-model", "hy4flash", "deepseek-v4.1-flash-sg"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func TestParseModelsModelsFallback(t *testing.T) {
	body := []byte(`{"data":{"models":[{"id":"a"},{"id":"b"}]}}`)
	ids, err := ParseModels(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
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
