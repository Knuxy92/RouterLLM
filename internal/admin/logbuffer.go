package admin

import (
	"strings"
	"sync"
)

const defaultLogCapacity = 500

type LogEntry struct {
	Seq  uint64 `json:"seq"`
	Line string `json:"line"`
}

type LogBuffer struct {
	mu       sync.Mutex
	entries  []LogEntry
	capacity int
	nextSeq  uint64
}

func NewLogBuffer() *LogBuffer {
	return &LogBuffer{capacity: defaultLogCapacity}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			b.append(line)
		}
	}

	return len(p), nil
}

func (b *LogBuffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSeq++
	b.entries = append(b.entries, LogEntry{Seq: b.nextSeq, Line: line})
	if len(b.entries) > b.capacity {
		b.entries = b.entries[len(b.entries)-b.capacity:]
	}
}

func (b *LogBuffer) Since(seq uint64) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]LogEntry, 0, len(b.entries))
	for _, e := range b.entries {
		if e.Seq > seq {
			out = append(out, e)
		}
	}

	return out
}
