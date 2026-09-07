package services

import (
	"context"
	"sync"
	"time"

	"routerllm/internal/telemetry"
)

type traceKey struct{}

// RequestTrace carries per-request telemetry between forward() (which owns the
// lifecycle: start, ttft, serve, record) and ForwardRaw/tryKeys (which learn
// the attempts and the serving provider/key). It rides on the request context
// so neither function signature has to change. Handlers outside services
// (e.g. /v1/messages) start one with WithRequestTrace and finish it with Event.
type RequestTrace struct {
	mu       sync.Mutex
	started  time.Time
	ttftMS   int64
	attempts []telemetry.Attempt
	provider string
	upstream string
	key      string
}

// WithRequestTrace starts a trace for one request and returns it alongside the
// context carrying it, so ForwardRaw can attach the attempts and the served leg.
func WithRequestTrace(ctx context.Context) (context.Context, *RequestTrace) {
	t := &RequestTrace{started: time.Now()}

	return context.WithValue(ctx, traceKey{}, t), t
}

func traceFromContext(ctx context.Context) *RequestTrace {
	t, _ := ctx.Value(traceKey{}).(*RequestTrace)

	return t
}

func (t *RequestTrace) setServed(provider, upstream, key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.provider = provider
	t.upstream = upstream
	t.key = key
}

// Event assembles the finished record. status is the outcome written to the
// client; tokensOut comes from the usage sniff when one ran.
func (t *RequestTrace) Event(model, requestID string, status int, err string, tokensOut int) telemetry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()

	e := telemetry.Event{
		Time:          t.started,
		Model:         model,
		RequestID:     requestID,
		Status:        status,
		Provider:      t.provider,
		UpstreamModel: t.upstream,
		Key:           t.key,
		TTFTMS:        t.ttftMS,
		DurationMS:    time.Since(t.started).Milliseconds(),
		TokensOut:     tokensOut,
		Err:           err,
		Attempts:      t.attempts,
	}

	return e
}
