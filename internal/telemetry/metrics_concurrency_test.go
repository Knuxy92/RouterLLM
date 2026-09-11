package telemetry

import (
	"sync"
	"testing"
	"time"
)

// Record mutates the ttft reservoir in place while Summary/Windows/LastError
// work on snapshots outside the metrics lock; the snapshot must own its slice.
// Run with -race.
func TestMetricsConcurrentRecordAndRead(t *testing.T) {
	m := NewMetrics()

	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 4000; i++ {
			e := testEvent("alpha", "up-a", 200, int64(10+i%40))
			e.Time = time.Now()
			m.Record(e)
		}
		close(done)
	}()

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}

				_ = m.Summary("p:alpha")
				_ = m.Windows("g", time.Hour, 3)
				_, _, _ = m.LastError("p:alpha", 24*time.Hour)
			}
		}()
	}

	wg.Wait()
}
