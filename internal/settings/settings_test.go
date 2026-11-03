package settings

import (
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() does not validate: %v", err)
	}
}

func TestValidateRejectsNonPositive(t *testing.T) {
	cases := map[string]func(*Settings){
		"request_body_bytes": func(s *Settings) { s.Limits.RequestBodyBytes = 0 },
		"upstream_tools":     func(s *Settings) { s.Limits.UpstreamTools = -1 },
		"ring_capacity":      func(s *Settings) { s.Telemetry.RingCapacity = 0 },
		"retention":          func(s *Settings) { s.Telemetry.Retention = 0 },
		"poll_ms":            func(s *Settings) { s.Admin.UI.PollMS = 0 },
		"session_ttl":        func(s *Settings) { s.Admin.SessionTTL = -time.Second },
		"watch_interval":     func(s *Settings) { s.Server.WatchInterval = 0 },
	}

	for name, mutate := range cases {
		live := Default()
		mutate(&live)
		if err := live.Validate(); err == nil {
			t.Errorf("%s: validation accepted a non-positive value", name)
		}
	}
}

func TestValidateRejectsContradictions(t *testing.T) {
	cases := map[string]func(*Settings){
		"tools over the upstream cap": func(s *Settings) { s.Limits.UpstreamTools = 129 },
		"rewrite above file":          func(s *Settings) { s.Telemetry.RewriteBytes = s.Telemetry.FileBytes },
		"timeout floor above ceiling": func(s *Settings) { s.Admin.Test.TimeoutFloorSeconds = 200 },
		"default timeout outside":     func(s *Settings) { s.Admin.Test.DefaultTimeoutSeconds = 1 },
		"default tokens above max":    func(s *Settings) { s.Admin.Test.DefaultTokens = s.Admin.Test.MaxTokens + 1 },
	}

	for name, mutate := range cases {
		live := Default()
		mutate(&live)
		if err := live.Validate(); err == nil {
			t.Errorf("%s: validation accepted a contradictory value", name)
		}
	}
}

func TestNormalizeDerivesRewriteBudget(t *testing.T) {
	live := Default()
	live.Telemetry.FileBytes = 8 << 20
	live.Telemetry.RewriteBytes = 0
	live.Normalize()

	if live.Telemetry.RewriteBytes != 4<<20 {
		t.Fatalf("rewrite_bytes = %d, want half of file_bytes", live.Telemetry.RewriteBytes)
	}
	if err := live.Validate(); err != nil {
		t.Fatalf("normalized settings do not validate: %v", err)
	}
}

func TestSetReplacesSnapshot(t *testing.T) {
	original := *Current()
	t.Cleanup(func() { Set(original) })

	next := Default()
	next.Limits.RequestBodyBytes = 1234
	Set(next)

	if got := Current().Limits.RequestBodyBytes; got != 1234 {
		t.Fatalf("Current().Limits.RequestBodyBytes = %d, want 1234", got)
	}

	// The previous snapshot must stay readable: a request in flight holds the
	// pointer it read, not a live view.
	if original.Limits.RequestBodyBytes == 1234 {
		t.Fatal("Set mutated the previously published snapshot")
	}
}
