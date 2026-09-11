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

func TestLoadDotenvStripsBOMAndExportPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "\uFEFFexport ROUTERLLM_TEST_BOM_KEY=bom-value\nROUTERLLM_TEST_PLAIN_KEY=plain-value\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ROUTERLLM_TEST_BOM_KEY", "ROUTERLLM_TEST_PLAIN_KEY"} {
		t.Setenv(key, "seed")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	if err := LoadDotenv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ROUTERLLM_TEST_BOM_KEY"); got != "bom-value" {
		t.Fatalf("ROUTERLLM_TEST_BOM_KEY = %q, want bom-value", got)
	}
	if got := os.Getenv("ROUTERLLM_TEST_PLAIN_KEY"); got != "plain-value" {
		t.Fatalf("ROUTERLLM_TEST_PLAIN_KEY = %q, want plain-value", got)
	}
}
