// Package telemetry records per-request routing outcomes and aggregates them
// into metrics. Events are metadata only — masked keys, no prompt or response
// bodies ever — and are persisted as JSON lines so dashboards survive restarts.
package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
	Attempts      []Attempt `json:"attempts,omitempty"`
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
