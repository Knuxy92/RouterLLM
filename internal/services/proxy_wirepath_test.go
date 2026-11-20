package services

import (
	"testing"
)

func TestWirePathStripsV1(t *testing.T) {
	for inbound, want := range map[string]string{
		"/v1/chat/completions": "/chat/completions",
		"/v1/responses":        "/responses",
		"/v1/messages":         "/messages",
	} {
		if got := wirePath(inbound); got != want {
			t.Errorf("wirePath(%q) = %q, want %q", inbound, got, want)
		}
	}
}