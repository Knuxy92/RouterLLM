package quota

import (
	"net/http"
	"testing"
)

func TestParseQuotaHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex-Primary-Used-Percent", "42")
	header.Set("X-Codex-Primary-Window-Minutes", "43200")
	header.Set("X-Codex-Primary-Reset-At", "1792294116")
	header.Set("X-Codex-Secondary-Used-Percent", "0")
	header.Set("X-Codex-Secondary-Window-Minutes", "0")
	header.Set("X-Codex-Plan-Type", "free")

	snapshot, ok := ParseHeaders(header)
	if !ok {
		t.Fatal("expected a snapshot")
	}
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 42 || snapshot.Primary.WindowMinutes != 43200 || snapshot.Primary.ResetAt != 1792294116 {
		t.Fatalf("primary = %+v", snapshot.Primary)
	}
	if snapshot.Secondary != nil {
		t.Fatalf("zero-length secondary window must be dropped: %+v", snapshot.Secondary)
	}
	if snapshot.PlanType != "free" {
		t.Fatalf("plan = %q, want free", snapshot.PlanType)
	}
}

func TestParseQuotaHeadersWithoutCodexHeaders(t *testing.T) {
	if _, ok := ParseHeaders(http.Header{}); ok {
		t.Fatal("plain headers must not produce a snapshot")
	}
	if _, ok := ParseHeaders(nil); ok {
		t.Fatal("nil headers must not produce a snapshot")
	}
}
