package codex

import (
	"reflect"
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
			name: "multiple slugs with duplicates",
			body: `{"models":[{"slug":"gpt-5","display_name":"GPT-5"},{"slug":"gpt-5","display_name":"GPT-5 again"},{"slug":"gpt-5-mini","display_name":"GPT-5 Mini"}]}`,
			want: []string{"gpt-5", "gpt-5-mini"},
		},
		{
			name: "empty models array",
			body: `{"models":[]}`,
			want: []string{},
		},
		{
			name:    "malformed JSON",
			body:    `{"models":[{"slug":"gpt-5"`,
			wantErr: true,
		},
		{
			name: "empty slug entries",
			body: `{"models":[{"slug":"","display_name":"blank"},{"slug":"gpt-5","display_name":"GPT-5"},{"slug":""},{"display_name":"missing slug"}]}`,
			want: []string{"gpt-5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModels([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseModels() error = nil, want error")
				}
				if !strings.Contains(err.Error(), "parse models catalog") {
					t.Errorf("ParseModels() error = %q, want wrapped context", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModels() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseModels() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
