package util

import (
	"bufio"
	"os"
	"strings"
)

func LoadDotenv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		if comment := strings.Index(val, " #"); comment >= 0 {
			val = strings.TrimSpace(val[:comment])
		}
		val = strings.Trim(val, `"'`)
		if _, ok := os.LookupEnv(key); !ok {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}
