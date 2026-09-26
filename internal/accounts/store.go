package accounts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// file is the on-disk wrapper shared by every account store: a JSON object
// holding a single "accounts" array. T is the per-style entry type.
type file[T any] struct {
	Accounts []T `json:"accounts"`
}

// DefaultPath returns the path from envName when set, else defaultName beside
// the executable.
func DefaultPath(envName, defaultName string) string {
	if path := os.Getenv(envName); path != "" {
		return path
	}

	exe, err := os.Executable()
	if err != nil {
		return defaultName
	}

	return filepath.Join(filepath.Dir(exe), defaultName)
}

// Load reads entries from an account file. A missing file is not an error: the
// caller may point the store at a path that does not exist yet.
func Load[T any](label, path string) ([]T, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s accounts %q: %w", label, path, err)
	}

	var f file[T]
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s accounts %q: %w", label, path, err)
	}

	return f.Accounts, nil
}

// Keys returns the non-empty key of every entry, in order.
func Keys[T any](entries []T, keyOf func(T) string) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if key := keyOf(entry); key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}

// Add inserts entry, replacing the first existing entry whose non-empty key
// matches, and persists. Entries are mutated in place before persisting, so a
// failed write still leaves the change in memory. Caller must hold the store's
// mutex.
func Add[T any](label, path string, entries *[]T, entry T, keyOf func(T) string) error {
	key := keyOf(entry)
	for i, existing := range *entries {
		if k := keyOf(existing); k != "" && k == key {
			(*entries)[i] = entry
			return Persist(label, path, *entries)
		}
	}
	*entries = append(*entries, entry)

	return Persist(label, path, *entries)
}

// Rotate rewrites every entry whose token equals oldToken. The file is re-read
// first so an account added by another process (a concurrent login) survives
// the rewrite instead of being dropped from disk. Caller must hold the store's
// mutex.
func Rotate[T any](label, path string, entries *[]T, oldToken, newToken string, tokenOf func(T) string, setToken func(*T, string)) error {
	if oldToken == newToken || newToken == "" {
		return nil
	}

	if err := mergeFile(label, path, entries, tokenOf); err != nil {
		return err
	}

	rotated := false
	for i := range *entries {
		if tokenOf((*entries)[i]) == oldToken {
			setToken(&(*entries)[i], newToken)
			rotated = true
		}
	}
	if !rotated {
		return nil
	}

	return Persist(label, path, *entries)
}

// mergeFile folds entries that appeared in the file since it was loaded into
// memory, keyed by tokenOf. A missing file is not an error.
func mergeFile[T any](label, path string, entries *[]T, tokenOf func(T) string) error {
	onDisk, err := Load[T](label, path)
	if err != nil {
		return err
	}

	known := make(map[string]bool, len(*entries))
	for _, entry := range *entries {
		known[tokenOf(entry)] = true
	}
	for _, entry := range onDisk {
		if token := tokenOf(entry); token != "" && !known[token] {
			*entries = append(*entries, entry)
			known[token] = true
		}
	}

	return nil
}

// Persist marshals entries into the wrapper shape and writes them to path.
func Persist[T any](label, path string, entries []T) error {
	raw, err := json.MarshalIndent(file[T]{Accounts: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s accounts: %w", label, err)
	}

	return WriteFileAtomic(label, path, raw, 0600)
}

// WriteFileAtomic writes through a temp file in the same directory and renames
// it over the target, so a crash mid-write cannot leave a truncated file behind.
func WriteFileAtomic(label, path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s accounts %q: %w", label, path, err)
	}

	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write %s accounts %q: %w", label, path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write %s accounts %q: %w", label, path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %s accounts %q: %w", label, path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %s accounts %q: %w", label, path, err)
	}

	return nil
}
