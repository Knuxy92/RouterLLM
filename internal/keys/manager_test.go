package keys

import (
	"testing"
	"time"
)

func TestSetDisabledByIndexSkipsKeyInNext(t *testing.T) {
	m := New([]string{"key-a", "key-b"}, time.Minute)
	if err := m.SetDisabledByIndex(0, true); err != nil {
		t.Fatalf("SetDisabledByIndex() error = %v", err)
	}

	for range 5 {
		got, ok := m.Next()
		if !ok || got != "key-b" {
			t.Fatalf("Next() = %q, %v; want key-b only", got, ok)
		}
	}
}

func TestMarkDeadDoesNotOverrideManualDisable(t *testing.T) {
	m := New([]string{"key-a"}, time.Minute)
	if err := m.SetDisabledByIndex(0, true); err != nil {
		t.Fatalf("SetDisabledByIndex() error = %v", err)
	}

	m.MarkDead("key-a")

	state := m.States()[0]
	if !state.Manual {
		t.Fatal("MarkDead cleared the manual disable flag")
	}
	if got, _ := m.Next(); got != "" {
		t.Fatalf("Next() = %q after MarkDead, want empty (key stays dead)", got)
	}
}

func TestSetDisabledByIndexReEnable(t *testing.T) {
	m := New([]string{"key-a", "key-b"}, time.Minute)
	if err := m.SetDisabledByIndex(0, true); err != nil {
		t.Fatalf("disable error = %v", err)
	}
	if got := m.AliveCount(); got != 1 {
		t.Fatalf("AliveCount() = %d after disable, want 1", got)
	}

	if err := m.SetDisabledByIndex(0, false); err != nil {
		t.Fatalf("enable error = %v", err)
	}
	if got := m.AliveCount(); got != 2 {
		t.Fatalf("AliveCount() = %d after re-enable, want 2", got)
	}
	if state := m.States()[0]; state.Manual || !state.Alive {
		t.Fatalf("state after re-enable = %+v, want alive and not manual", state)
	}
}

func TestSetDisabledByIndexOutOfRange(t *testing.T) {
	m := New([]string{"key-a"}, time.Minute)

	if err := m.SetDisabledByIndex(3, true); err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestManualDisableSurvivesRestore(t *testing.T) {
	m := New([]string{"key-a", "key-b"}, time.Minute)
	if err := m.SetDisabledByIndex(1, true); err != nil {
		t.Fatalf("SetDisabledByIndex() error = %v", err)
	}

	snapshot := m.Snapshot()

	reborn := New([]string{"key-a", "key-b"}, time.Minute)
	reborn.Restore(snapshot)

	if got, _ := reborn.Next(); got != "key-a" {
		t.Fatalf("Next() = %q after restore, want key-a (manual disable lost)", got)
	}
	if state := reborn.States()[1]; !state.Manual {
		t.Fatal("manual flag not reconstructed on Restore")
	}
	if err := reborn.SetDisabledByIndex(1, false); err != nil {
		t.Fatalf("re-enable after restore error = %v", err)
	}
	if got := reborn.AliveCount(); got != 2 {
		t.Fatalf("AliveCount() = %d after re-enable, want 2", got)
	}
}
