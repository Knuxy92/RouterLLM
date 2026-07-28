package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenvRemovesInlineComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("ROUTERLLM_LOG_FILE=routerllm.log  # file logging\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROUTERLLM_LOG_FILE", "")
	if err := os.Unsetenv("ROUTERLLM_LOG_FILE"); err != nil {
		t.Fatal(err)
	}

	if err := LoadDotenv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ROUTERLLM_LOG_FILE"); got != "routerllm.log" {
		t.Fatalf("ROUTERLLM_LOG_FILE = %q, want routerllm.log", got)
	}
}
