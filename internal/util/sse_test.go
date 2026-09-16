package util

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func streamBody(t *testing.T, src string, filter bool) string {
	t.Helper()

	rec := httptest.NewRecorder()

	if err := StreamSSE(strings.NewReader(src), rec, filter); err != nil {
		t.Fatalf("StreamSSE returned error: %v", err)
	}

	return rec.Body.String()
}

func TestStreamSSEForwardsErrorFrame(t *testing.T) {
	src := "data: {\"error\":{\"message\":\"boom\",\"type\":\"stream_error\"}}\n\n"

	got := streamBody(t, src, true)

	if !strings.Contains(got, `"stream_error"`) {
		t.Fatalf("expected error frame to pass through, got %q", got)
	}

	if strings.Contains(got, "[DONE]") {
		t.Fatalf("must not append [DONE] when upstream sent none, got %q", got)
	}
}

func TestStreamSSEFiltersJunk(t *testing.T) {
	src := "data: {\"foo\":\"bar\"}\n\ndata: {\"choices\":[]}\n\n"

	got := streamBody(t, src, true)

	if strings.Contains(got, `"foo"`) {
		t.Fatalf("expected junk frame to be filtered, got %q", got)
	}

	if !strings.Contains(got, `"choices"`) {
		t.Fatalf("expected choices frame to pass through, got %q", got)
	}
}

func TestStreamSSEChunkAndDoneFlow(t *testing.T) {
	src := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n"

	got := streamBody(t, src, true)

	if !strings.Contains(got, `"choices"`) {
		t.Fatalf("expected chunk to pass through, got %q", got)
	}

	if !strings.Contains(got, "data: [DONE]") {
		t.Fatalf("expected [DONE] to be forwarded, got %q", got)
	}
}

func TestStreamSSENonDoneClose(t *testing.T) {
	src := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"

	got := streamBody(t, src, true)

	if strings.Contains(got, "[DONE]") {
		t.Fatalf("must not append [DONE] on non-DONE close, got %q", got)
	}
}
