package telemetry

import (
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
