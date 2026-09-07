package telemetry

import (
	"math"
	"sort"
	"sync"
	"time"
)

// bucketStats accumulates one 5-minute slice of traffic for one series key.
type bucketStats struct {
	req      int
	err      int
	tokens   int
	ttft     []int64 // capped samples for percentile estimates
	duration int64   // summed for tokens/sec denominators
}

func (b *bucketStats) add(e Event) {
	b.req++
	if e.Status >= 400 || e.Status == 0 {
		b.err++
	}
	b.tokens += e.TokensOut
	b.duration += e.DurationMS
	if e.TTFTMS > 0 {
		if len(b.ttft) < 64 {
			b.ttft = append(b.ttft, e.TTFTMS)
		} else {
			// Reservoir-of-one replacement keeps late samples fresh without
			// growing the slice; percentiles stay estimates.
			b.ttft[len(b.ttft)-1] = e.TTFTMS
		}
	}
}

// Metrics aggregates events into per-key 5-minute buckets. Series keys are
// "p:<provider>", "l:<provider>/<upstream_model>", "m:<model_id>" and "g" for
// global.
type Metrics struct {
	mu      sync.Mutex
	buckets map[string]map[int64]*bucketStats
}

func NewMetrics() *Metrics {
	return &Metrics{buckets: make(map[string]map[int64]*bucketStats)}
}

func bucketStart(t time.Time) int64 {
	return t.Unix() / int64(bucketWidth/time.Second) * int64(bucketWidth/time.Second)
}

func seriesKeys(e Event) []string {
	keys := []string{"g"}
	if e.Model != "" {
		keys = append(keys, "m:"+e.Model)
	}
	if e.Provider != "" {
		keys = append(keys, "p:"+e.Provider)
		keys = append(keys, "l:"+e.Provider+"/"+e.UpstreamModel)
	}

	return keys
}

// Record folds one event into every series it touches.
func (m *Metrics) Record(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	start := bucketStart(e.Time)
	for _, key := range seriesKeys(e) {
		series, ok := m.buckets[key]
		if !ok {
			series = make(map[int64]*bucketStats)
			m.buckets[key] = series
		}
		b, ok := series[start]
		if !ok {
			b = &bucketStats{}
			series[start] = b
		}
		b.add(e)
	}
}

// Window is one contiguous slice of a series.
type Window struct {
	Start   int64 `json:"start"` // unix seconds
	Req     int   `json:"req"`
	Err     int   `json:"err"`
	Tokens  int   `json:"tokens"`
	TTFTP50 int64 `json:"ttft_p50_ms,omitempty"`
	TTFTP95 int64 `json:"ttft_p95_ms,omitempty"`
}

func (m *Metrics) series(name string) map[int64]*bucketStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.buckets[name]
	if !ok {
		return nil
	}
	out := make(map[int64]*bucketStats, len(src))
	for k, v := range src {
		cp := *v
		out[k] = &cp
	}

	return out
}

// Windows merges the series into count contiguous windows of the given width
// ending at the current bucket (oldest first), dropping empty windows.
func (m *Metrics) Windows(name string, width time.Duration, count int) []Window {
	src := m.series(name)
	if src == nil {
		return nil
	}

	now := time.Now()
	current := bucketStart(now)
	oldest := bucketStart(now.Add(-width * time.Duration(count-1)))

	out := make([]Window, 0, count)
	for start := oldest; start <= current; start += int64(width / time.Second) {
		var acc bucketStats
		for bStart, b := range src {
			if bStart >= start && bStart < start+int64(width/time.Second) {
				acc.req += b.req
				acc.err += b.err
				acc.tokens += b.tokens
				acc.duration += b.duration
				acc.ttft = append(acc.ttft, b.ttft...)
			}
		}
		if acc.req == 0 {
			continue
		}
		out = append(out, Window{
			Start:   start,
			Req:     acc.req,
			Err:     acc.err,
			Tokens:  acc.tokens,
			TTFTP50: percentile(acc.ttft, 50),
			TTFTP95: percentile(acc.ttft, 95),
		})
	}

	return out
}

// Summary is the rolled-up 24h view of one series.
type Summary struct {
	Req       int     `json:"req"`
	Err       int     `json:"err"`
	Success   float64 `json:"success_pct"`
	TTFTP50   int64   `json:"ttft_p50_ms"`
	TTFTP95   int64   `json:"ttft_p95_ms"`
	Tokens    int     `json:"tokens"`
	UptimePct float64 `json:"uptime_pct"`
	TokPerSec float64 `json:"tok_per_sec"`
}

// Summary rolls the series over the trailing 24h.
func (m *Metrics) Summary(name string) Summary {
	src := m.series(name)
	now := time.Now()
	dayAgo := bucketStart(now.Add(-24 * time.Hour))

	var acc bucketStats
	up := 0
	total := 0
	for start, b := range src {
		if start < dayAgo {
			continue
		}
		acc.req += b.req
		acc.err += b.err
		acc.tokens += b.tokens
		acc.duration += b.duration
		acc.ttft = append(acc.ttft, b.ttft...)
		total++
		if b.err < b.req {
			up++
		}
	}

	s := Summary{
		Req:     acc.req,
		Err:     acc.err,
		TTFTP50: percentile(acc.ttft, 50),
		TTFTP95: percentile(acc.ttft, 95),
		Tokens:  acc.tokens,
	}
	if acc.req > 0 {
		s.Success = math.Round((1-float64(acc.err)/float64(acc.req))*1000) / 10
	}
	if total > 0 {
		s.UptimePct = math.Round(float64(up)/float64(total)*1000) / 10
	}
	if acc.duration > 0 && acc.tokens > 0 {
		s.TokPerSec = math.Round(float64(acc.tokens)/(float64(acc.duration)/1000)*10) / 10
	}

	return s
}

// LastFailure walks a series' buckets backwards to surface the most recent
// erroring bucket for leg badges ("503 spike" style notes on the UI). It
// returns the bucket start and the error count, newest first.
func (m *Metrics) LastError(name string, since time.Duration) (int64, int, bool) {
	src := m.series(name)
	cutoff := bucketStart(time.Now().Add(-since))

	var starts []int64
	for start := range src {
		if start >= cutoff {
			starts = append(starts, start)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] > starts[j] })

	for _, start := range starts {
		if b := src[start]; b.err > 0 {
			return start, b.err, true
		}
	}

	return 0, 0, false
}

// Prune drops buckets older than the retention window.
func (m *Metrics) Prune() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := bucketStart(time.Now().Add(-retention))
	for key, series := range m.buckets {
		for start := range series {
			if start < cutoff {
				delete(series, start)
			}
		}
		if len(series) == 0 {
			delete(m.buckets, key)
		}
	}
}

func percentile(samples []int64, p float64) int64 {
	if len(samples) == 0 {
		return 0
	}

	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return sorted[idx]
}
