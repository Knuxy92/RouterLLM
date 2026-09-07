// Package telemetry records per-request routing outcomes and aggregates them
// into metrics. Events carry masked keys and routing metadata; failed requests
// additionally capture the upstream error response body (truncated), never
// request bodies or successful responses. Events persist as JSON lines so
// dashboards survive restarts.
package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// Event ring kept in RAM for the admin console.
	ringCapacity = 2000
	// 5-minute metric buckets, retained a week back.
	bucketWidth = 5 * time.Minute
	retention   = 7 * 24 * time.Hour
	// Rotate the jsonl once it grows past this (bytes).
	maxFileSize = 10 << 20
)

type Attempt struct {
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	Key       string `json:"key,omitempty"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Note      string `json:"note,omitempty"`
	RespBody  string `json:"resp_body,omitempty"`
}

type Event struct {
	Seq           uint64    `json:"seq"`
	Time          time.Time `json:"time"`
	Model         string    `json:"model"`
	RequestID     string    `json:"request_id,omitempty"`
	Status        int       `json:"status"`
	Provider      string    `json:"provider,omitempty"`
	UpstreamModel string    `json:"upstream_model,omitempty"`
	Key           string    `json:"key,omitempty"`
	TTFTMS        int64     `json:"ttft_ms"`
	DurationMS    int64     `json:"duration_ms"`
	TokensOut     int       `json:"tokens_out"`
	Err           string    `json:"err,omitempty"`
	RespBody      string    `json:"resp_body,omitempty"`
	Attempts      []Attempt `json:"attempts,omitempty"`
}

// RespBodyCap bounds a captured upstream error body per event/attempt.
const RespBodyCap = 2 << 10

// ClampBody truncates a captured upstream response body for storage. Error
// bodies are the debugging payload of a failed request — the thing that says
// WHY upstream refused — so they are kept (truncated); successful responses
// are still never captured.
func ClampBody(b []byte, limit int) string {
	if len(b) == 0 {
		return ""
	}
	s := string(b)
	if len(s) > limit {
		return s[:limit] + " …(truncated)"
	}

	return s
}

// Level derives the UI badge from the final status: a completed relay is
// info, upstream failures are warn (we retried or failed over) and terminal
// errors are error.
func (e Event) Level() string {
	switch {
	case e.Status == 0:
		return "error"
	case e.Status < 400:
		return "info"
	case e.Status == 429 || (e.Status >= 500 && e.Status < 600) || len(e.Attempts) > 1:
		return "warn"
	default:
		return "error"
	}
}

type Store struct {
	mu      sync.Mutex
	ring    []Event
	next    uint64
	path    string
	file    *os.File
	size    int64
	metrics *Metrics
}

// NewStore opens (or creates) the jsonl at path and replays the retained tail
// into the ring and metrics. A nil/empty path keeps everything in memory —
// handy for tests.
func NewStore(path string) (*Store, error) {
	s := &Store{metrics: NewMetrics()}
	if path == "" {
		return s, nil
	}

	if err := s.load(path); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	s.path = path
	s.file = f
	s.size = info.Size()

	return s, nil
}

// NewMemStore returns an in-memory store with no disk persistence — the
// nil-safe default so telemetry calls never need a nil check.
func NewMemStore() *Store {
	s, _ := NewStore("")

	return s
}

// load replays the tail of an existing jsonl. Events older than the retention
// window are skipped, and the file is compacted when the majority is stale so
// restart time stays bounded.
func (s *Store) load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	now := time.Now()
	var kept []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var e Event
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Time.IsZero() {
			continue
		}
		if now.Sub(e.Time) > retention {
			continue
		}
		kept = append(kept, e)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	for _, e := range kept {
		s.remember(e)
	}

	// Compact when more than a quarter of the file is stale or it is oversized;
	// compaction rewrites only the retained events.
	stale := len(kept) == 0
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > maxFileSize {
		stale = true
	}
	if stale || s.pruneNeeded(kept) {
		return s.rewrite(kept)
	}

	return nil
}

func (s *Store) pruneNeeded(kept []Event) bool {
	if info, err := os.Stat(s.path); err != nil || info.Size() <= maxFileSize/2 {
		return false
	}
	_ = kept
	return true
}

// rewrite atomically replaces the jsonl with the retained events.
func (s *Store) rewrite(kept []Event) error {
	if s.path == "" {
		return nil
	}

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	var size int64
	for _, e := range kept {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		n, err := f.Write(append(line, '\n'))
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		size += int64(n)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	if s.file != nil {
		s.file.Close()
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}

	s.file, _ = os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	s.size = size

	return nil
}

func (s *Store) remember(e Event) Event {
	s.next++
	e.Seq = s.next
	s.ring = append(s.ring, e)
	if len(s.ring) > ringCapacity {
		s.ring = s.ring[len(s.ring)-ringCapacity:]
	}
	s.metrics.Record(e)

	return e
}

// Record stores one request outcome: ring, metrics and (if configured) disk.
func (s *Store) Record(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e = s.remember(e)
	if s.file == nil {
		return
	}

	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	n, err := s.file.Write(append(line, '\n'))
	if err != nil {
		return
	}
	s.size += int64(n)
	if s.size >= maxFileSize {
		s.rewrite(s.ring)
	}
}

// Since returns every buffered event newer than seq (the console polls with
// the last seq it saw; 0 returns the full ring).
func (s *Store) Since(seq uint64) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Event, 0, len(s.ring))
	for _, e := range s.ring {
		if e.Seq > seq {
			out = append(out, e)
		}
	}

	return out
}

// QueryOpts filters and slices the ring for the console's request-log table.
// Zero values mean "no filter". Levels is a set of accepted levels (empty set
// = all levels).
type QueryOpts struct {
	Provider string
	Model    string
	Levels   map[string]bool
	Text     string
	NotBefore time.Time
	Page     int // 1-based; <=0 treated as 1
	PerPage  int // <=0 treated as 50, capped at the ring size
}

// Page is one slice of Query results, newest events first.
type Page struct {
	Entries    []Event `json:"entries"`
	Page       int     `json:"page"`
	PerPage    int     `json:"per_page"`
	Total      int     `json:"total"`
	TotalPages int     `json:"total_pages"`
	ErrorTotal int     `json:"error_total"` // errors in the filtered set ignoring the level filter
	Latest     uint64  `json:"latest"`
}

// Query returns one page of filtered events, newest first.
func (s *Store) Query(o QueryOpts) Page {
	s.mu.Lock()
	defer s.mu.Unlock()

	perPage := o.PerPage
	if perPage <= 0 {
		perPage = 50
	}
	if perPage > ringCapacity {
		perPage = ringCapacity
	}
	page := o.Page
	if page <= 0 {
		page = 1
	}
	text := strings.ToLower(o.Text)

	// Newest first: walk the ring backwards.
	var matched []Event
	for i := len(s.ring) - 1; i >= 0; i-- {
		e := s.ring[i]
		if !o.NotBefore.IsZero() && e.Time.Before(o.NotBefore) {
			continue
		}
		if o.Provider != "" && e.Provider != o.Provider {
			continue
		}
		if o.Model != "" && e.Model != o.Model {
			continue
		}
		if len(o.Levels) > 0 && !o.Levels[e.Level()] {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(e.Msg()), text) {
			continue
		}
		matched = append(matched, e)
	}

	total := len(matched)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	end := start + perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	entries := make([]Event, end-start)
	copy(entries, matched[start:end])

	errorTotal := 0
	for _, e := range matched {
		if e.Level() == "error" {
			errorTotal++
		}
	}

	return Page{
		Entries:    entries,
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		ErrorTotal: errorTotal,
		Latest:     s.next,
	}
}

// Msg renders the event the way the log table shows it: the human sentence
// derived from outcome and routing. Kept in sync with Level().
func (e Event) Msg() string {
	if e.Err != "" {
		return fmt.Sprintf("%s → %s: %s", e.Model, e.Provider, e.Err)
	}
	parts := []string{"routed " + e.Model}
	if e.Provider != "" {
		parts = append(parts, "→ "+e.Provider)
	}
	if e.Key != "" {
		parts = append(parts, "(key "+e.Key+")")
	}
	if e.TokensOut > 0 {
		parts = append(parts, fmt.Sprintf("· %d tokens out", e.TokensOut))
	}

	return strings.Join(parts, " ")
}

// Latest returns the highest seq handed out so far.
func (s *Store) Latest() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.next
}

// Metrics exposes the aggregated view.
func (s *Store) Metrics() *Metrics {
	return s.metrics
}

// Close flushes and releases the file handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil

	return err
}

// DefaultPath puts the jsonl next to the config file so Docker mounts that
// already persist the yaml persist telemetry too.
func DefaultPath(configPath string) string {
	if configPath == "" {
		return ""
	}

	return filepath.Join(filepath.Dir(configPath), "routerllm-telemetry.jsonl")
}
