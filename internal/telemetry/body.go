package telemetry

import (
	"bytes"
	"io"
	"strconv"
	"sync"
	"time"
)

// usageTailBounds how much of the previous read is re-scanned so a usage
// object straddling a read boundary is still found.
const usageTail = 4 << 10

var usageFields = [][]byte{
	[]byte(`"completion_tokens":`),
	[]byte(`"output_tokens":`),
	[]byte(`"candidatesTokenCount":`),
}

// Watcher wraps an upstream response body and passively captures two numbers
// while the bytes flow through: time-to-first-read (TTFT) and the largest
// token-count field seen in the stream. The scan is dialect-agnostic — it
// matches the counters emitted by OpenAI chunks (completion_tokens), Anthropic
// message_delta (output_tokens) and Gemini usageMetadata (candidatesTokenCount)
// — and reads nothing else, so no request or response content is retained.
// The caller owns recording: read Tokens()/TTFTMS() after the body is drained.
type Watcher struct {
	src io.ReadCloser

	mu        sync.Mutex
	started   time.Time
	firstRead time.Time
	tail      []byte
	tokens    int
}

// Watch starts timing immediately.
func Watch(src io.ReadCloser) *Watcher {
	return &Watcher{src: src, started: time.Now()}
}

func (w *Watcher) Read(p []byte) (int, error) {
	n, err := w.src.Read(p)

	w.mu.Lock()
	if w.firstRead.IsZero() && n > 0 {
		w.firstRead = time.Now()
	}
	w.scan(p[:n])
	w.mu.Unlock()

	return n, err
}

func (w *Watcher) Close() error {
	return w.src.Close()
}

// Tokens returns the largest completion/output token count seen so far.
func (w *Watcher) Tokens() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.tokens
}

// TTFTMS is the delay until the first byte was read, 0 while nothing arrived.
func (w *Watcher) TTFTMS() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.firstRead.IsZero() {
		return 0
	}

	return w.firstRead.Sub(w.started).Milliseconds()
}

// scan looks for token-count fields in the new bytes plus a bounded tail of
// the previous read, so values split across reads are still caught.
func (w *Watcher) scan(chunk []byte) {
	if len(chunk) == 0 {
		return
	}

	buf := chunk
	if len(w.tail) > 0 {
		buf = append(w.tail, chunk...)
	}

	for _, field := range usageFields {
		idx := bytes.LastIndex(buf, field)
		if idx < 0 {
			continue
		}
		rest := buf[idx+len(field):]
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			continue
		}
		if n, err := strconv.Atoi(string(rest[:end])); err == nil && n > w.tokens {
			w.tokens = n
		}
	}

	if len(buf) > usageTail {
		buf = buf[len(buf)-usageTail:]
	}
	w.tail = append(w.tail[:0], buf...)
}
