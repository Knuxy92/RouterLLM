package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEvent(provider, upstream string, status int, ttft int64) Event {
	return Event{
		Time:          time.Now(),
		Model:         "m1",
		Provider:      provider,
		UpstreamModel: upstream,
		Status:        status,
		TTFTMS:        ttft,
		DurationMS:    1000,
		TokensOut:     100,
	}
}

func TestRecordKeepsRingAndSeq(t *testing.T) {
	s, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}

	for range ringCapacity + 50 {
		s.Record(testEvent("p", "u", 200, 100))
	}

	if got := len(s.ring); got != ringCapacity {
		t.Fatalf("ring size = %d, want %d", got, ringCapacity)
	}
	if s.ring[0].Seq != uint64(50)+1 {
		t.Fatalf("oldest seq = %d, want 51 (drop-oldest)", s.ring[0].Seq)
	}
	if s.Latest() != uint64(ringCapacity+50) {
		t.Fatalf("Latest() = %d", s.Latest())
	}
}

func TestSinceFiltersBySeq(t *testing.T) {
	s, _ := NewStore("")
	for range 5 {
		s.Record(testEvent("p", "u", 200, 10))
	}

	got := s.Since(3)
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("Since(3) = %d entries, first seq %d; want 2 entries starting at 4", len(got), got[0].Seq)
	}
	if len(s.Since(0)) != 5 {
		t.Fatal("Since(0) should return the whole ring")
	}
}

func TestPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	s.Record(testEvent("alpha", "up-a", 200, 120))
	s.Record(Event{Time: time.Now(), Model: "m", Provider: "alpha", Status: 502, Err: "upstream exploded"})
	s.Close()

	reborn, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reborn.Close()

	got := reborn.Since(0)
	if len(got) != 2 {
		t.Fatalf("reloaded %d events, want 2", len(got))
	}
	if got[1].Status != 502 || got[1].Err != "upstream exploded" {
		t.Fatalf("event fields lost across reload: %+v", got[1])
	}
	if got[0].Key != "" {
		t.Fatal("events must never carry a raw key")
	}
}

func TestJSONLContainsNoRawSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := testEvent("alpha", "up-a", 200, 5)
	e.Key = "…abcd" // caller masks before Record; store must not add more fields
	s.Record(e)

	raw, _ := os.ReadFile(path)
	var decoded map[string]any
	if json.Unmarshal(raw[:len(raw)-1], &decoded) != nil {
		t.Fatalf("file is not valid jsonl: %s", raw)
	}
	for _, banned := range []string{"prompt", "messages", "body", "authorization"} {
		if _, ok := decoded[banned]; ok {
			t.Fatalf("jsonl contains forbidden field %q", banned)
		}
	}
}

func TestRotateOnOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	big := strings.Repeat("x", 4096)
	for range 3000 { // > 10 MiB of lines
		s.Record(Event{Time: time.Now(), Model: big, Status: 200})
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxFileSize+1<<20 {
		t.Fatalf("file did not rotate: %d bytes", info.Size())
	}
}

func TestSummaryAndWindows(t *testing.T) {
	m := NewMetrics()
	base := time.Now().Add(-time.Hour)

	for i := range 10 {
		e := testEvent("alpha", "up-a", 200, int64(100+i))
		e.Time = base.Add(time.Duration(i) * time.Minute)
		e.DurationMS = 2000
		e.TokensOut = 20
		m.Record(e)
	}
	bad := testEvent("alpha", "up-a", 503, 900)
	bad.Time = base.Add(30 * time.Minute)
	m.Record(bad)

	sum := m.Summary("p:alpha")
	if sum.Req != 11 || sum.Err != 1 {
		t.Fatalf("Summary = %+v, want req 11 err 1", sum)
	}
	if sum.TTFTP50 == 0 || sum.TTFTP95 == 0 {
		t.Fatalf("percentiles missing: %+v", sum)
	}
	if sum.UptimePct <= 0 || sum.UptimePct > 100 {
		t.Fatalf("uptime out of range: %v", sum.UptimePct)
	}
	if sum.TokPerSec == 0 {
		t.Fatal("tok/s missing though tokens+duration recorded")
	}

	windows := m.Windows("p:alpha", time.Hour, 3)
	if len(windows) == 0 {
		t.Fatal("Windows() empty")
	}
}

func TestLastErrorNewestFirst(t *testing.T) {
	m := NewMetrics()
	old := time.Now().Add(-3 * time.Hour)
	recent := time.Now().Add(-30 * time.Minute)

	e1 := testEvent("alpha", "up-a", 503, 100)
	e1.Time = old
	m.Record(e1)

	e2 := testEvent("beta", "up-b", 429, 100)
	e2.Time = recent
	m.Record(e2)

	start, errs, ok := m.LastError("p:beta", 24*time.Hour)
	if !ok || errs == 0 || start != bucketStart(recent) {
		t.Fatalf("LastError(beta) = %v, %d, %v", start, errs, ok)
	}
	if _, _, ok := m.LastError("p:alpha", time.Hour); ok {
		t.Fatal("alpha error is older than the window, should be absent")
	}
}

func TestLegSeriesIsolated(t *testing.T) {
	m := NewMetrics()
	m.Record(testEvent("alpha", "up-a", 200, 50))
	m.Record(testEvent("beta", "up-b", 200, 60))

	if s := m.Summary("l:alpha/up-a"); s.Req != 1 {
		t.Fatalf("leg series polluted: %+v", s)
	}
	if s := m.Summary("g"); s.Req != 2 {
		t.Fatalf("global series wrong: %+v", s)
	}
}

func TestFileEventsCarrySeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Record(testEvent("alpha", "up-a", 200, 10))
	s.Record(testEvent("alpha", "up-a", 200, 11))

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			t.Fatalf("invalid jsonl line: %s", scanner.Text())
		}
		got = append(got, e.Seq)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("file seqs = %v, want [1 2] — the ring and the jsonl must agree", got)
	}
}

func TestQueryPagedAndFiltered(t *testing.T) {
	s, _ := NewStore("")
	base := time.Now()

	// 30 events: 24 ok + 6 error, alternating providers, spread over hours.
	for i := range 30 {
		e := testEvent("alpha", "up-a", 200, int64(10+i))
		e.Time = base.Add(-time.Duration(i) * time.Hour)
		e.Model = "m"
		if i%5 == 0 {
			e = testEvent("beta", "up-b", 402, 900) // 4xx (non-429) levels as "error"
			e.Time = base.Add(-time.Duration(i) * time.Hour)
			e.Model = "m"
			e.Err = "boom"
		}
		s.Record(e)
	}

	full := s.Query(QueryOpts{PerPage: 10})
	if full.Total != 30 || full.TotalPages != 3 || len(full.Entries) != 10 {
		t.Fatalf("full = total %d pages %d len %d; want 30/3/10", full.Total, full.TotalPages, len(full.Entries))
	}
	if full.Entries[0].Seq != 30 || full.Entries[9].Seq != 21 {
		t.Fatalf("page 1 not newest-first: first %d last %d", full.Entries[0].Seq, full.Entries[9].Seq)
	}

	page3 := s.Query(QueryOpts{Page: 3, PerPage: 10})
	if len(page3.Entries) != 10 || page3.Entries[0].Seq != 10 {
		t.Fatalf("page 3 = len %d first seq %d; want 10 events starting at seq 10", len(page3.Entries), page3.Entries[0].Seq)
	}

	errs := s.Query(QueryOpts{Levels: map[string]bool{"error": true}, PerPage: 10})
	if errs.Total != 6 || errs.ErrorTotal != 6 {
		t.Fatalf("error filter = total %d errTotal %d; want 6/6", errs.Total, errs.ErrorTotal)
	}

	byProv := s.Query(QueryOpts{Provider: "beta"})
	if byProv.Total != 6 {
		t.Fatalf("provider filter total = %d, want 6", byProv.Total)
	}

	windowed := s.Query(QueryOpts{NotBefore: base.Add(-5 * time.Hour)})
	if windowed.Total != 6 {
		t.Fatalf("hours window total = %d, want 6 (events 0-5h old)", windowed.Total)
	}

	text := s.Query(QueryOpts{Text: "BOOM"})
	if text.Total != 6 {
		t.Fatalf("text filter total = %d, want 6 (case-insensitive)", text.Total)
	}

	// page beyond the end clamps to the last page
	clamped := s.Query(QueryOpts{Page: 99, PerPage: 10})
	if clamped.Page != 3 {
		t.Fatalf("page clamp = %d, want 3", clamped.Page)
	}
}

func TestWindowsFullSpanAndHourAlignment(t *testing.T) {
	m := NewMetrics()

	// traffic in exactly two windows: 5h ago and now
	e1 := testEvent("alpha", "up-a", 200, 100)
	e1.Time = time.Now().Add(-5 * time.Hour)
	m.Record(e1)

	e2 := testEvent("alpha", "up-a", 200, 120)
	e2.Time = time.Now()
	m.Record(e2)

	windows := m.Windows("g", 2*time.Hour, 12)

	if len(windows) != 12 {
		t.Fatalf("windows = %d, want 12 (empty ones included, full 24h span)", len(windows))
	}

	// every boundary sits on a clock hour
	for _, w := range windows {
		if w.Start%3600 != 0 {
			t.Fatalf("window start %d not on a clock-hour boundary", w.Start)
		}
	}

	// oldest starts 22h before the current hour; newest is the current hour
	nowHour := time.Now().Truncate(time.Hour).Unix()
	if windows[0].Start != nowHour-22*3600 {
		t.Fatalf("oldest window start = %d, want %d", windows[0].Start, nowHour-22*3600)
	}
	if windows[len(windows)-1].Start != nowHour {
		t.Fatalf("newest window start = %d, want %d", windows[len(windows)-1].Start, nowHour)
	}

	// the two busy windows carry the events; the rest are zero
	busy := 0
	for _, w := range windows {
		if w.Req > 0 {
			busy++
		}
	}
	if busy != 2 {
		t.Fatalf("busy windows = %d, want 2", busy)
	}
	if windows[len(windows)-1].Req != 1 {
		t.Fatalf("current window req = %d, want 1", windows[len(windows)-1].Req)
	}
}

// Daily windows anchor to local midnight, so a "Sep 8" bucket really is the
// calendar day — not a 24h slice ending at whatever hour the process started.
func TestWeeklyWindowsAnchorToMidnight(t *testing.T) {
	m := NewMetrics()

	now := time.Now()
	e := testEvent("alpha", "up-a", 200, 100)
	e.Time = now
	m.Record(e)

	windows := m.Windows("g", 24*time.Hour, 7)

	if len(windows) != 7 {
		t.Fatalf("windows = %d, want 7", len(windows))
	}
	for _, w := range windows {
		d := time.Unix(w.Start, 0)
		if d.Hour() != 0 || d.Minute() != 0 || d.Second() != 0 {
			t.Fatalf("daily window start %d is not local midnight (%s)", w.Start, d)
		}
	}
	if last := time.Unix(windows[6].Start, 0); last.Format("2006-01-02") != now.Format("2006-01-02") {
		t.Fatalf("newest daily window = %s, want today %s", last, now.Format("2006-01-02"))
	}
}

// The dashboard's 24h/7d toggle needs a real week-long rollup: p50 and tok/s
// cannot be reconstructed client-side from the daily windows, so the backend
// serves a second Summary over a wider span.
func TestSummarySinceSpansBeyondADay(t *testing.T) {
	m := NewMetrics()

	old := testEvent("alpha", "up-a", 200, 150)
	old.Time = time.Now().Add(-72 * time.Hour)
	m.Record(old)

	if got := m.Summary("g").Req; got != 0 {
		t.Fatalf("24h summary req = %d, want 0 (event is 3 days old)", got)
	}
	if got := m.SummarySince("g", time.Now().Add(-7*24*time.Hour)).Req; got != 1 {
		t.Fatalf("7d summary req = %d, want 1", got)
	}
}

// One oversized client-controlled field must not reach the ring or the jsonl:
// a line the replay cannot read would silently disable disk persistence until
// the file is repaired by hand.
func TestRecordClampsOversizedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	huge := strings.Repeat("x", 1<<20)
	s.Record(Event{
		Time:      time.Now(),
		Model:     huge,
		RequestID: huge,
		Status:    502,
		Err:       huge,
		RespBody:  huge,
		Attempts: []Attempt{
			{Provider: "alpha", Status: 502, Note: huge, RespBody: huge},
		},
	})

	buffered := s.Since(0)
	if len(buffered) != 1 {
		t.Fatalf("ring holds %d events, want 1", len(buffered))
	}
	assertClampedEvent(t, buffered[0])
	s.Close()

	reborn, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen after oversized record: %v", err)
	}
	defer reborn.Close()

	got := reborn.Since(0)
	if len(got) != 1 {
		t.Fatalf("replayed %d events, want 1", len(got))
	}
	assertClampedEvent(t, got[0])

	for _, line := range jsonlLines(t, path) {
		if len(line) >= maxLineSize {
			t.Fatalf("jsonl line = %d bytes, must stay readable by the replay", len(line))
		}
	}
}

func assertClampedEvent(t *testing.T, e Event) {
	t.Helper()

	if len(e.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(e.Attempts))
	}

	checks := []struct {
		field string
		value string
		limit int
	}{
		{"model", e.Model, maxFieldSize},
		{"request_id", e.RequestID, maxFieldSize},
		{"err", e.Err, maxErrSize},
		{"resp_body", e.RespBody, RespBodyCap},
		{"attempt note", e.Attempts[0].Note, maxFieldSize},
		{"attempt resp_body", e.Attempts[0].RespBody, RespBodyCap},
	}
	for _, c := range checks {
		if len(c.value) == 0 || len(c.value) > c.limit+16 {
			t.Fatalf("%s length = %d, want clamped to (0, %d]", c.field, len(c.value), c.limit+16)
		}
	}
}

// A single unreadable line must not abort the replay: NewStore has to come up,
// keep the valid events, and compact the garbage away.
func TestLoadSkipsOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")

	valid1, err := json.Marshal(Event{Time: time.Now(), Model: "m1", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	valid2, err := json.Marshal(Event{Time: time.Now(), Model: "m2", Status: 500})
	if err != nil {
		t.Fatal(err)
	}

	var file bytes.Buffer
	file.Write(valid1)
	file.WriteByte('\n')
	file.WriteString(strings.Repeat("g", 2<<20))
	file.WriteByte('\n')
	file.WriteString("{not json}\n")
	file.Write(valid2)
	file.WriteByte('\n')
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dirtySize := int64(file.Len())

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore with oversized line: %v", err)
	}
	defer s.Close()

	got := s.Since(0)
	if len(got) != 2 || got[0].Model != "m1" || got[1].Model != "m2" {
		t.Fatalf("replay = %+v, want m1 and m2", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= dirtySize/2 {
		t.Fatalf("file not compacted: %d bytes, was %d", info.Size(), dirtySize)
	}
	for _, line := range jsonlLines(t, path) {
		if len(line) >= maxLineSize {
			t.Fatalf("oversized line survived compaction: %d bytes", len(line))
		}
	}

	// The compacted file must replay cleanly.
	again, err := NewStore(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer again.Close()
	if n := len(again.Since(0)); n != 2 {
		t.Fatalf("second replay = %d events, want 2", n)
	}
}

func jsonlLines(t *testing.T, path string) [][]byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var lines [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}

	return lines
}
