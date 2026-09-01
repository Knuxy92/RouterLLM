package config

import (
	"context"
	"crypto/sha256"
	"log"
	"os"
	"sync"
	"time"
)

const watchInterval = 3 * time.Second

func ConfigPath() string {
	if path := os.Getenv("ROUTERLLM_CONFIG_FILE"); path != "" {
		return path
	}

	return "routerllm.yaml"
}

func Hash(path string) [32]byte {
	sum, err := hashFile(path)
	if err != nil {
		return [32]byte{}
	}

	return sum
}

// Reloader owns the content hash of the config file, so the poll loop and any
// explicit reload (admin API write-back) share one gate. Whoever observes the
// new content first applies it; the other sees an unchanged hash and no-ops.
// Without this single owner a write via the admin API would be applied twice —
// once by the API and again by the next poll tick.
type Reloader struct {
	path   string
	logger *log.Logger
	apply  func(*Config)
	reject func(error)

	mu      sync.Mutex
	current [32]byte
}

func NewReloader(path string, baseline [32]byte, logger *log.Logger, apply func(*Config), reject func(error)) *Reloader {
	return &Reloader{
		path:    path,
		logger:  logger,
		apply:   apply,
		reject:  reject,
		current: baseline,
	}
}

// Reload applies the file if its contents changed since the last successful
// apply. Unchanged content is a no-op and reports success.
func (r *Reloader) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		r.fail(err)

		return err
	}

	next := sha256.Sum256(data)
	if next == r.current {
		return nil
	}

	return r.applyLocked(data, next)
}

func (r *Reloader) applyLocked(data []byte, next [32]byte) error {
	cfg, err := ParseBytes(data)
	if err != nil {
		r.current = next
		r.fail(err)

		return err
	}

	r.current = next
	r.apply(cfg)

	return nil
}

func (r *Reloader) fail(err error) {
	if r.logger != nil {
		r.logger.Printf("config reload rejected: %v", err)
	}

	if r.reject != nil {
		r.reject(err)
	}
}

// Watch polls the config file every watchInterval. It compares SHA-256 of the
// contents rather than mtime, because mtime propagation through Docker Desktop
// bind mounts on Windows is unreliable.
func (r *Reloader) Watch(ctx context.Context) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.Reload()
		}
	}
}

func hashFile(path string) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, err
	}

	return sha256.Sum256(data), nil
}
