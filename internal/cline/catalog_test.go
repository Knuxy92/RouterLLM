package cline

import (
	"slices"
	"strings"
	"testing"
)

func TestParseModels(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "all buckets with duplicate across buckets",
			body: `{"clinePass":[{"id":"cline-pass/glm-5.2"}],"free":[{"id":"reflection/beam-1.0"},{"id":"cline-pass/glm-5.2"}],"clineCloud":[{"id":"cline-cloud/claude-sonnet-4.6","name":"Claude Sonnet 4.6"}]}`,
			want: []string{"cline-pass/glm-5.2", "reflection/beam-1.0", "cline-cloud/claude-sonnet-4.6"},
		},
		{
			name: "only one bucket",
			body: `{"free":[{"id":"a"},{"id":"b"}]}`,
			want: []string{"a", "b"},
		},
		{
			name: "unknown bucket only",
			body: `{"mystery":[{"id":"should-not-appear"}]}`,
			want: []string{},
		},
		{
			name:    "malformed JSON",
			body:    `{not valid json`,
			wantErr: true,
		},
		{
			name: "empty id entries",
			body: `{"clinePass":[{"id":""},{"id":"valid"},{"id":""}],"clineCloud":[{"id":""}]}`,
			want: []string{"valid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModels([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "parse cline recommended models") {
					t.Errorf("error not descriptive: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
