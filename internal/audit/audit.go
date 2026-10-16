// Package audit records the full request journey — what arrived from the
// client, what the router made of it, what was sent upstream, what came back,
// what the translator produced, and what went out on the wire — as one JSON
// object per phase.
//
// It is a leaf package (stdlib only) so both internal/routers and
// internal/services can record into the same trace without importing each
// other: routers already reaches services through handlers, so the reverse
// edge would close a cycle.
//
// A trace is created per request by the router middleware. Inner layers look
// it up from the context and append an entry; the middleware drains it once
// the handler returns, so phases appear in the log in the order they happened
// even when an entry is completed late (a streamed upstream body is only fully
// read after the response headers are known).
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Phase names, in the order a healthy request produces them. prov_req and
// prov_rep repeat once per upstream attempt, so a request that fails over
// shows several pairs.
const (
	PhaseClientReq = "client_req"
	PhaseServerReq = "server_req"
	PhaseProvReq   = "prov_req"
	PhaseProvRep   = "prov_rep"
	PhaseServerRep = "server_rep"
	PhaseClientRep = "client_rep"
)

// BodyLimit caps how much of any one body is kept. Beyond it the entry is
// marked truncated and the rest is dropped.
const BodyLimit = 10 << 20

// prettyLimit is the size past which a body is logged compact rather than
// indented — a 10 MiB payload pretty-printed would swamp the log.
const prettyLimit = 256 << 10

// Entry is one phase of the journey. Fields that do not apply to a phase stay
// zero and are omitted from the JSON.
type Entry struct {
	Phase      string      `json:"phase"`
	At         time.Time   `json:"at"`
	ElapsedMS  int64       `json:"elapsed_ms"`
	RequestID  string      `json:"request_id,omitempty"`
	Method     string      `json:"method,omitempty"`
	URL        string      `json:"url,omitempty"`
	Provider   string      `json:"provider,omitempty"`
	Model      string      `json:"model,omitempty"`
	Upstream   string      `json:"upstream_model,omitempty"`
	Dialect    string      `json:"dialect,omitempty"`
	Attempt    int         `json:"attempt,omitempty"`
	Status     int         `json:"status,omitempty"`
	DurationMS int64       `json:"duration_ms,omitempty"`
	Headers    http.Header `json:"headers,omitempty"`

	mu             sync.Mutex
	body           bytes.Buffer
	truncated      bool
	emitted        bool
	emittedBodyLen int
}

// Write appends captured body bytes, keeping only the first BodyLimit.
func (e *Entry) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.body.Len() >= BodyLimit {
		e.truncated = true
		return len(p), nil
	}
	if room := BodyLimit - e.body.Len(); len(p) > room {
		e.body.Write(p[:room])
		e.truncated = true

		return len(p), nil
	}
	e.body.Write(p)

	return len(p), nil
}

// MarkTruncated flags the body as cut short at the cap.
func (e *Entry) MarkTruncated() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.truncated = true
}

// Truncated reports whether the body hit the cap.
func (e *Entry) Truncated() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.truncated
}

// Bytes returns a copy of the captured body.
func (e *Entry) Bytes() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]byte(nil), e.body.Bytes()...)
}

// Trace collects the phases of one request.
type Trace struct {
	mu      sync.Mutex
	entries []*Entry
	logger  *log.Logger
	start   time.Time
	reqID   string
}

type traceKey struct{}

// WithTrace returns a context carrying tr and the trace itself.
func WithTrace(ctx context.Context, logger *log.Logger, reqID string) (context.Context, *Trace) {
	tr := &Trace{logger: logger, start: time.Now(), reqID: reqID}

	return context.WithValue(ctx, traceKey{}, tr), tr
}

// FromContext returns the trace on ctx, or nil when auditing is off.
func FromContext(ctx context.Context) *Trace {
	tr, _ := ctx.Value(traceKey{}).(*Trace)

	return tr
}

// Start is when the request reached the router.
func (t *Trace) Start() time.Time {
	if t == nil {
		return time.Time{}
	}

	return t.start
}

// Add appends an entry stamped with the elapsed time since the request began
// and returns it. Nothing is logged yet — fill in what the layer knows, then
// call Emit. An entry may be emitted twice: once as soon as the phase happens,
// and again later if a streamed body filled it in.
func (t *Trace) Add(phase string) *Entry {
	if t == nil {
		return nil
	}

	e := &Entry{
		Phase:     phase,
		At:        time.Now(),
		ElapsedMS: time.Since(t.start).Milliseconds(),
		RequestID: t.reqID,
	}

	t.mu.Lock()
	t.entries = append(t.entries, e)
	t.mu.Unlock()

	return e
}

// Emit renders e as indented JSON and writes it to the log immediately, so a
// long request is watchable while it runs rather than only once it returns.
func (t *Trace) Emit(e *Entry) {
	if t == nil || t.logger == nil || e == nil {
		return
	}

	out := e.render()
	if len(out) == 0 || !e.markEmitted() {
		return
	}

	t.logger.Printf("%s", out)
}

// emitted tracks whether an entry has already been written to the log, so a
// second Emit only happens when there is something new to say.
func (e *Entry) markEmitted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Only worth writing again when the body grew: prov_rep is emitted when
	// the headers land and again when the stream finishes, and a streamed
	// body is read in many chunks.
	if e.emitted && e.body.Len() == e.emittedBodyLen {
		return false
	}
	e.emitted = true
	e.emittedBodyLen = e.body.Len()

	return true
}

func (e *Entry) render() []byte {
	e.mu.Lock()
	raw := append([]byte(nil), e.body.Bytes()...)
	truncated := e.truncated
	e.mu.Unlock()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(e); err != nil {
		return nil
	}
	buf.Truncate(buf.Len() - 1) // drop the encoder's newline

	var withBody map[string]any
	if err := json.Unmarshal(buf.Bytes(), &withBody); err != nil {
		return nil
	}

	if len(raw) == 0 && !truncated {
		return indent(withBody)
	}

	body, ok := prettyJSON(raw, len(raw) <= prettyLimit)
	if !ok {
		body = string(raw)
	}
	withBody["body"] = body
	if truncated {
		withBody["body_truncated"] = true
	}

	if len(raw) > prettyLimit {
		var compact bytes.Buffer
		if err := json.NewEncoder(&compact).Encode(withBody); err != nil {
			return nil
		}

		return bytes.TrimRight(compact.Bytes(), "\n")
	}

	return indent(withBody)
}

func indent(v map[string]any) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil
	}

	return bytes.TrimRight(out.Bytes(), "\n")
}

// RedactHeaders returns a copy with credential headers masked, so an audit
// line never carries an API key, bearer token or cookie.
func RedactHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}

	clean := h.Clone()
	for name := range clean {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "x-api-key" ||
			lower == "cookie" || lower == "cookie2" || strings.Contains(lower, "token") {
			clean.Set(name, "[REDACTED]")
		}
	}

	return clean
}

// prettyJSON re-indents valid JSON so the logged body is readable. A body that
// is not JSON is returned unquoted for the caller to escape as a string.
func prettyJSON(raw []byte, indent bool) (any, bool) {
	if !json.Valid(raw) {
		return nil, false
	}

	if !indent {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, false
		}

		var v any
		if err := json.Unmarshal(compact.Bytes(), &v); err != nil {
			return nil, false
		}

		return v, true
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return nil, false
	}

	var v any
	if err := json.Unmarshal(indented.Bytes(), &v); err != nil {
		return nil, false
	}

	return v, true
}

// TeeBody wraps rc so everything read through it also lands in e, and re-emits
// e once the body is done. The upstream response is the one body that cannot be
// known when its phase is recorded: the headers arrive before the stream does.
func TeeBody(rc io.ReadCloser, e *Entry, done func()) io.ReadCloser {
	if e == nil {
		return rc
	}

	return &teeBody{rc: rc, entry: e, done: done}
}

type teeBody struct {
	rc    io.ReadCloser
	entry *Entry
	done  func()
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		_, _ = t.entry.Write(p[:n])
	}

	return n, err
}

func (t *teeBody) Close() error {
	err := t.rc.Close()
	if t.done != nil {
		t.done()
	}

	return err
}
