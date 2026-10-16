package audit

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
)

func emitEntry(t *testing.T, phase string, mutate func(e *Entry)) string {
	t.Helper()

	var out bytes.Buffer
	_, tr := WithTrace(context.Background(), log.New(&out, "", 0), "req-1")
	e := tr.Add(phase)
	mutate(e)
	tr.Emit(e)

	return out.String()
}

func TestEmitEmbedsSmallJSONBody(t *testing.T) {
	body := `{"model":"m","messages":[]}`

	logs := emitEntry(t, PhaseClientReq, func(e *Entry) {
		e.Method = http.MethodPost
		e.URL = "/v1/chat/completions"
		_, _ = e.Write([]byte(body))
	})

	for _, want := range []string{
		fmt.Sprintf("[client_req]  POST /v1/chat/completions  id=req-1  body=%dB", len(body)),
		`"phase": "client_req"`,
		`"body": {`,
		`"model": "m"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}

	if strings.Contains(logs, "---- body:") {
		t.Errorf("a small JSON body belongs inside the block, not in a section:\n%s", logs)
	}
}

func TestEmitClipsLargeStreamBody(t *testing.T) {
	var stream strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&stream, "data: {\"i\":%d,\"text\":\"%s\"}\n\n", i, strings.Repeat("x", 60))
	}

	logs := emitEntry(t, PhaseClientRep, func(e *Entry) {
		e.Status = http.StatusOK
		e.DurationMS = 102506
		_, _ = e.Write([]byte(stream.String()))
	})

	for _, want := range []string{
		"[client_rep]  200  102.5s  id=req-1  body=",
		`"phase": "client_rep"`,
		"---- body: text,",
		`data: {"i":0,`,
		`data: {"i":2999,`,
		"[ +",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}

	if strings.Contains(logs, `\"`) {
		t.Errorf("stream body was JSON-escaped instead of shown raw:\n%s", logs)
	}

	for _, line := range strings.Split(logs, "\n") {
		if len(line) > 4096 {
			t.Fatalf("a %d-char line survived — the body was not clipped", len(line))
		}
	}
}

func TestEmitLargeJSONBodyUsesSection(t *testing.T) {
	big := `{"data":"` + strings.Repeat("x", 100000) + `"}`

	logs := emitEntry(t, PhaseServerReq, func(e *Entry) {
		e.Method = http.MethodPost
		e.URL = "/v1/chat/completions"
		_, _ = e.Write([]byte(big))
	})

	for _, want := range []string{
		"---- body: json,",
		`"body_bytes": `,
		"[ +",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}

	if strings.Contains(logs, `"body": `) {
		t.Errorf("an oversized JSON body must not be embedded in the block:\n%s", logs)
	}
}

func TestEmitMarksCaptureTruncatedBody(t *testing.T) {
	logs := emitEntry(t, PhaseProvRep, func(e *Entry) {
		e.Provider = "opencode"
		e.Status = http.StatusOK
		_, _ = e.Write([]byte(`{"partial":true}`))
		e.MarkTruncated()
	})

	for _, want := range []string{
		"[prov_rep]  200  provider=opencode",
		`"body_truncated": true`,
		"---- body: json,",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
}

func TestEmitIsIdempotentUntilBodyGrows(t *testing.T) {
	var out bytes.Buffer
	_, tr := WithTrace(context.Background(), log.New(&out, "", 0), "req-1")
	e := tr.Add(PhaseProvRep)
	e.Provider = "opencode"

	tr.Emit(e)
	first := out.Len()

	tr.Emit(e)
	if out.Len() != first {
		t.Fatalf("second emit without body growth wrote again:\n%s", out.String())
	}

	_, _ = e.Write([]byte("data: tail\n"))
	tr.Emit(e)
	if out.Len() == first {
		t.Fatal("emit after the body grew wrote nothing")
	}
}
