package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fileSize(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.Size()
}

// Rotation must leave headroom: a rewrite keeps only the newest events within
// half the cap. Writing the whole ring instead left the file at or above the
// cap and re-serialized it on every subsequent Record.
func TestRotationKeepsAppendHeadroom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := strings.Repeat("b", RespBodyCap)
	attempts := make([]Attempt, 3)
	for i := range attempts {
		attempts[i] = Attempt{Provider: "upstream", Model: "model", Status: 502, RespBody: body}
	}
	big := Event{Time: time.Now(), Model: "m", Provider: "alpha", Status: 502, Attempts: attempts}

	line, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(line))*ringCapacity <= maxFileSize {
		t.Fatalf("payload too small: serialized ring = %d bytes, need > %d", len(line)*ringCapacity, maxFileSize)
	}

	// Every Record must return with the file below the cap; an unbounded ring
	// rewrite leaves it at the (oversized) ring's size instead.
	for i := range ringCapacity + 300 {
		s.Record(big)
		if size := fileSize(t, path); size >= maxFileSize {
			t.Fatalf("record %d left the file at %d bytes, want < %d (rewrite kept the whole ring)", i, size, maxFileSize)
		}
	}

	// Record until a rotation is observed, then stop right after it.
	rotated := false
	prev := fileSize(t, path)
	for i := 0; i < 5000 && !rotated; i++ {
		s.Record(big)
		size := fileSize(t, path)
		if size < prev {
			rotated = true
		}
		prev = size
	}
	if !rotated {
		t.Fatal("rotation never compacted the file")
	}
	if prev >= maxFileSize {
		t.Fatalf("post-rotation size = %d, want < %d", prev, maxFileSize)
	}

	// The rotation left headroom: records must append and grow the file
	// instead of a rewrite resetting it on every call.
	for i := range 20 {
		s.Record(testEvent("alpha", "up-a", 200, 10))
		got := fileSize(t, path)
		if got <= prev {
			t.Fatalf("record %d did not grow the file: %d <= %d (rewrite storm)", i, got, prev)
		}
		prev = got
	}

	if n := len(s.Since(0)); n != ringCapacity {
		t.Fatalf("ring = %d events, want %d", n, ringCapacity)
	}
}
