package util

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func streamBody(t *testing.T, src string, filter bool) string {
	t.Helper()

	rec := httptest.NewRecorder()

	if err := StreamSSETransform(strings.NewReader(src), rec, filter, nil); err != nil {
		t.Fatalf("StreamSSETransform returned error: %v", err)
	}

	return rec.Body.String()
}

func TestStreamSSESanitizesErrorFrame(t *testing.T) {
	src := "data: {\"error\":{\"message\":\"failed to invoke model 'meta/muse-spark' for org_2ue3sRj via OpenRouter: rate limited\",\"type\":\"stream_error\",\"code\":\"stream_initialization_failed\"}}\n\n"

	got := streamBody(t, src, true)

	if !strings.Contains(got, `"error"`) {
		t.Fatalf("client must still receive an error frame, got %q", got)
	}
	for _, leaked := range []string{"stream_initialization_failed", "meta/muse-spark", "org_2ue3sRj", "OpenRouter", "rate limited"} {
		if strings.Contains(got, leaked) {
			t.Errorf("error frame leaked upstream detail %q: %s", leaked, got)
		}
	}
	if strings.Contains(got, "[DONE]") {
		t.Fatalf("must not append [DONE] when upstream sent none, got %q", got)
	}
}

func TestStreamSSEKeepsNormalFramesAroundSanitizedError(t *testing.T) {
	src := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"error\":{\"message\":\"boom\",\"type\":\"stream_error\"}}\n\n" +
		"data: [DONE]\n"

	got := streamBody(t, src, true)

	if !strings.Contains(got, `"content":"hi"`) {
		t.Fatalf("expected the content frame to pass through untouched, got %q", got)
	}
	if strings.Contains(got, "boom") {
		t.Fatalf("upstream error message leaked: %q", got)
	}
	if strings.Count(got, `"error"`) != 1 {
		t.Fatalf("expected exactly one error frame, got %q", got)
	}
	if !strings.Contains(got, "data: [DONE]") {
		t.Fatalf("expected [DONE] to be forwarded, got %q", got)
	}
}

func TestParseErrorFrame(t *testing.T) {
	frame, ok := ParseErrorFrame(`{"error":{"code":"stream_error","type":"upstream_error","message":"boom"}}`)
	if !ok {
		t.Fatal("ParseErrorFrame = false, want true for an error payload")
	}
	if frame.Code != "stream_error" || frame.Type != "upstream_error" || frame.Message != "boom" {
		t.Fatalf("frame = %+v, want the upstream fields", frame)
	}

	if _, ok := ParseErrorFrame(`{"choices":[{"delta":{"content":"hi"}}]}`); ok {
		t.Fatal("ParseErrorFrame = true for a normal chunk, want false")
	}
	if _, ok := ParseErrorFrame("not json"); ok {
		t.Fatal("ParseErrorFrame = true for a non-JSON payload, want false")
	}
	if _, ok := ParseErrorFrame(`{"choices":[]}`); ok {
		t.Fatal("ParseErrorFrame = true for an empty choices chunk, want false")
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
