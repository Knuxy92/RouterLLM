// Package audit records the full request journey — what arrived from the
// client, what the router made of it, what was sent upstream, what came back,
// what the translator produced, and what went out on the wire.
//
// It is a leaf package (stdlib only) so both internal/routers and
// internal/services can record into the same trace without importing each
// other: routers already reaches services through handlers, so the reverse
// edge would close a cycle.
//
// A trace is created per request by the router middleware. Inner layers look
// it up from the context and append an entry; each entry is written as soon
// as its phase happens. Every entry has the same three-part shape: a one-line
// header for scanning, the metadata as indented JSON, and — only when the
// body cannot sit inside the JSON — a raw text section below it. A small JSON
// body is embedded in the block; an SSE stream, plain text, or an oversized
// body becomes text, clipped head-and-tail, so no entry can turn into a
// single multi-megabyte line.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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

// bodyRenderLimit caps how much of one body reaches the log. A body over the
// limit is shown head and tail — half each, aligned to line boundaries — with
// the omitted middle replaced by a marker, so the entry stays scannable
// instead of collapsing into one enormous line.
const bodyRenderLimit = 64 << 10

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

// bodyLen reports how many body bytes are captured so far.
func (e *Entry) bodyLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.body.Len()
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
// call Emit.
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

// Emit writes the entry to the log immediately — header line, metadata block,
// body section — so a long request is watchable while it runs rather than
// only once it returns. An entry may be emitted twice: once when its phase
// happens, and again once a streamed body has filled it in. A second emit
// only happens when the body grew, so nothing is written twice unchanged.
func (t *Trace) Emit(e *Entry) {
	if t == nil || t.logger == nil || e == nil {
		return
	}

	out := e.render()
	if len(out) == 0 || !e.markEmitted() {
		return
	}

	t.logger.Printf("%s\n\n", out)
}

// markEmitted tracks whether an entry has already been written, so a repeat
// emit only happens when there is something new to say: prov_rep is emitted
// when the headers land and again when the stream finishes.
func (e *Entry) markEmitted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.emitted && e.body.Len() == e.emittedBodyLen {
		return false
	}
	e.emitted = true
	e.emittedBodyLen = e.body.Len()

	return true
}

// render builds the entry's log block. The header line carries the facts at a
// glance, the JSON below it carries the detail, and the optional text section
// at the end carries a body that has no place inside the JSON. There is no
// path that collapses the entry into one giant line.
func (e *Entry) render() []byte {
	e.mu.Lock()
	raw := append([]byte(nil), e.body.Bytes()...)
	truncated := e.truncated
	e.mu.Unlock()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil
	}
	buf.Truncate(buf.Len() - 1) // drop the encoder's newline

	var meta map[string]any
	if err := json.Unmarshal(buf.Bytes(), &meta); err != nil {
		return nil
	}

	section := renderBody(meta, raw, truncated)

	var out bytes.Buffer
	out.WriteString(e.headerLine())
	out.WriteString("\n")
	out.Write(indent(meta))
	if len(section) > 0 {
		out.WriteString("\n")
		out.Write(section)
	}

	return out.Bytes()
}

// renderBody puts the body where it stays readable: a small JSON body is
// decoded into the metadata and rendered inside the block; everything else —
// a stream, plain text, an oversized or capture-truncated payload — becomes a
// raw text section, unescaped, instead of a wall of \n and \" escapes.
func renderBody(meta map[string]any, raw []byte, truncated bool) []byte {
	if len(raw) == 0 && !truncated {
		return nil
	}

	if obj, ok := parseJSON(raw); ok && len(raw) <= bodyRenderLimit && !truncated {
		meta["body"] = obj

		return nil
	}

	kind, text := "text", raw
	if indented, ok := indentJSON(raw); ok {
		kind, text = "json", indented
	}

	meta["body_bytes"] = len(raw)
	if truncated {
		meta["body_truncated"] = true
	}

	shown, omitted := clip(text, bodyRenderLimit)

	var section bytes.Buffer
	fmt.Fprintf(&section, "---- body: %s, %s", kind, fmtSize(len(raw)))
	if omitted > 0 {
		fmt.Fprintf(&section, " (first + last %s shown)", fmtSize(bodyRenderLimit/2))
	}
	section.WriteString(" ----\n")
	section.Write(shown)

	return section.Bytes()
}

// clip returns at most limit bytes of text — half from the start, half from
// the end — falling on line boundaries so frames and JSON lines are never cut
// in half. The omitted middle is replaced by a marker line.
func clip(text []byte, limit int) ([]byte, int) {
	if len(text) <= limit {
		return text, 0
	}

	head := limit / 2
	tail := limit - head

	headEnd := head
	if i := bytes.LastIndexByte(text[:head], '\n'); i >= 0 {
		headEnd = i + 1
	}

	tailStart := len(text) - tail
	if i := bytes.IndexByte(text[tailStart:], '\n'); i >= 0 {
		tailStart += i + 1
	}

	omitted := tailStart - headEnd

	var out bytes.Buffer
	out.Write(text[:headEnd])
	fmt.Fprintf(&out, "[ +%s omitted ]\n", fmtSize(omitted))
	out.Write(text[tailStart:])

	return out.Bytes(), omitted
}

// headerLine is the one-line summary above every block — phase first, then
// the facts that identify this phase — so the file can be scanned and tailed
// without reading any JSON.
func (e *Entry) headerLine() string {
	parts := []string{"[" + e.Phase + "]"}

	if e.Method != "" {
		parts = append(parts, e.Method+" "+e.URL)
	}
	if e.Status != 0 {
		parts = append(parts, strconv.Itoa(e.Status))
	}
	if e.DurationMS > 0 {
		parts = append(parts, fmtDur(e.DurationMS))
	}
	if e.Provider != "" {
		parts = append(parts, "provider="+e.Provider)
	}
	if e.Model != "" {
		parts = append(parts, "model="+e.Model)
	}
	if e.Upstream != "" {
		parts = append(parts, "upstream="+e.Upstream)
	}
	if e.Dialect != "" {
		parts = append(parts, "dialect="+e.Dialect)
	}
	if e.Attempt > 0 {
		parts = append(parts, "attempt="+strconv.Itoa(e.Attempt))
	}
	if e.RequestID != "" {
		parts = append(parts, "id="+e.RequestID)
	}
	if n := e.bodyLen(); n > 0 {
		parts = append(parts, "body="+fmtSize(n))
	}

	return strings.Join(parts, "  ")
}

// fmtSize renders a byte count for header and marker lines.
func fmtSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// fmtDur renders a millisecond duration for header lines.
func fmtDur(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}

	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func indent(v map[string]any) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil
	}

	return bytes.TrimRight(out.Bytes(), "\n")
}

// parseJSON reports whether raw holds a JSON value and returns it decoded, so
// it can be embedded in the metadata block as a real JSON value.
func parseJSON(raw []byte) (any, bool) {
	if !json.Valid(raw) {
		return nil, false
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}

	return v, true
}

// indentJSON re-indents a JSON body as readable text for the raw body section.
func indentJSON(raw []byte) ([]byte, bool) {
	if !json.Valid(raw) {
		return nil, false
	}

	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, false
	}

	return out.Bytes(), true
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
