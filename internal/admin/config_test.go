package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"routerllm/internal/settings"
)

func TestHandleConfigServesLiveSettings(t *testing.T) {
	original := *settings.Current()
	t.Cleanup(func() { settings.Set(original) })

	live := settings.Default()
	live.Admin.UI.PollMS = 1500
	live.Admin.UI.LogPerPage = 7
	live.Admin.Test.MaxTokens = 2048
	live.Admin.Test.DefaultPrompt = "ping"
	live.Telemetry.RingCapacity = 512
	settings.Set(live)

	rec := httptest.NewRecorder()
	Deps{}.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var payload ConfigPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if payload.UI.PollMS != 1500 {
		t.Errorf("ui.poll_ms = %d, want 1500", payload.UI.PollMS)
	}
	if payload.UI.LogPerPage != 7 {
		t.Errorf("ui.log_per_page = %d, want 7", payload.UI.LogPerPage)
	}
	if payload.Test.MaxTokens != 2048 {
		t.Errorf("test.max_tokens = %d, want 2048", payload.Test.MaxTokens)
	}
	if payload.Test.DefaultPrompt != "ping" {
		t.Errorf("test.default_prompt = %q, want ping", payload.Test.DefaultPrompt)
	}
	if payload.RingCapacity != 512 {
		t.Errorf("ring_capacity = %d, want 512", payload.RingCapacity)
	}
	if len(payload.Efforts) != len(validReasoningEffort) {
		t.Errorf("efforts = %v, want every whitelisted effort", payload.Efforts)
	}
	if len(payload.StyleCalls) != len(validStyleCall) {
		t.Errorf("stylecalls = %v, want every whitelisted call style", payload.StyleCalls)
	}
}
