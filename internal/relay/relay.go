// Package relay manages a child Bun process that forwards opencode gateway
// traffic through Bun's TLS stack. The opencode free-tier gateway scores the
// client at the transport level — from datacenter IPs only Bun-family clients
// pass its structural gate — so an opencode provider's base_url is rewritten
// to the local relay when OPENCODE_TLS_RELAY is enabled. Any relay failure
// falls back to the untouched base_url (direct connection); request handling
// never blocks on the relay.
package relay

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed bun-relay.ts
var relayScript string

// DefaultUpstream is where the relay forwards traffic when the provider's
// base_url already points at a relay (legacy manual setup) or cannot be
// parsed.
const DefaultUpstream = "https://opencode.ai"

const (
	envEnabled  = "OPENCODE_TLS_RELAY"
	envPort     = "OPENCODE_RELAY_PORT"
	envBin      = "OPENCODE_RELAY_BIN"
	envUpstream = "OPENCODE_RELAY_UPSTREAM"

	// legacyPort is the manual systemd relay's default port; a base_url
	// pointing there predates this feature and must not be double-wrapped.
	legacyPort = 9999

	healthBudget = 6 * time.Second
	healthTick   = 150 * time.Millisecond
)

// Enabled reports whether the relay feature is switched on.
func Enabled() bool {
	return os.Getenv(envEnabled) == "1"
}

// ConfiguredPort returns the fixed relay port from the environment. An unset
// or "0" value means the manager picks a free port — the legacy manual-relay
// port (9999) is never assumed here because it may still be occupied by a
// standalone relay from before this feature existed.
func ConfiguredPort() int {
	if v := strings.TrimSpace(os.Getenv(envPort)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// UpstreamFor resolves the relay's upstream origin for a provider base_url:
// the forced env value wins; a base_url already pointing at our own relay
// port is treated as the legacy manual setup (forward to the default
// upstream); otherwise the base_url's own origin is used.
func UpstreamFor(baseURL string) string {
	if forced := strings.TrimSpace(os.Getenv(envUpstream)); forced != "" {
		return strings.TrimRight(forced, "/")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return DefaultUpstream
	}
	if parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" {
		if port := parsed.Port(); port != "" {
			if n, err := strconv.Atoi(port); err == nil && n == ConfiguredPort() {
				return DefaultUpstream
			}
		}
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
}

// Manager supervises one bun child per upstream origin.
type Manager struct {
	log *log.Logger
	out io.Writer

	mu    sync.Mutex
	procs map[string]*proc // upstream origin -> supervised child
	ports map[string]int   // upstream origin -> sticky port across respawns
}

type proc struct {
	upstream string
	port     int
	backoff  time.Duration

	ctx    context.Context // proc lifetime; canceled by Stop
	cancel context.CancelFunc
	done   chan struct{} // closed when the supervise loop exits

	mu        sync.Mutex
	cmd       *exec.Cmd       // current child, replaced on respawn
	logFile   *os.File        // child stdio sink; file handles don't stall test exit
	supCtx    context.Context // per-spawn lifetime; cancels the current child
	supCancel context.CancelFunc
}

var (
	defaultOnce sync.Once
	defaultM    *Manager
)

// Default returns the process-wide manager used by config loading, so
// hot-reload Ensure calls are idempotent against the same children.
func Default() *Manager {
	defaultOnce.Do(func() {
		defaultM = New(log.New(os.Stderr, "relay: ", log.LstdFlags), os.Stderr)
	})
	return defaultM
}

func New(log *log.Logger, out io.Writer) *Manager {
	return &Manager{
		log:   log,
		out:   out,
		procs: map[string]*proc{},
		ports: map[string]int{},
	}
}

func relayBin() string {
	if bin := strings.TrimSpace(os.Getenv(envBin)); bin != "" {
		return bin
	}
	return "bun"
}

// Ensure returns a local relay origin ("http://127.0.0.1:<port>") that
// forwards to the given upstream origin, spawning the child when needed. It
// is safe to call on every config load: an already-supervised child for the
// same upstream is reused.
func (m *Manager) Ensure(ctx context.Context, upstream string) (string, error) {
	upstream = strings.TrimRight(strings.TrimSpace(upstream), "/")
	if upstream == "" {
		return "", errors.New("relay: empty upstream")
	}

	m.mu.Lock()
	if p, ok := m.procs[upstream]; ok && p.ctx.Err() == nil {
		m.mu.Unlock()
		return p.baseURL(), nil
	}
	m.mu.Unlock()
	delete(m.procs, upstream)

	port, err := m.portFor(upstream)
	if err != nil {
		return "", err
	}

	p := &proc{
		upstream: upstream,
		port:     port,
		backoff:  time.Second,
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.done = make(chan struct{})

	if err := m.startProc(p); err != nil {
		m.mu.Lock()
		delete(m.ports, upstream)
		m.mu.Unlock()
		return "", err
	}
	go m.supervise(p)

	m.mu.Lock()
	m.procs[upstream] = p
	m.mu.Unlock()

	return p.baseURL(), nil
}

// Resolve maps a provider base_url onto the relay: the origin is rewritten to
// a supervised relay child and any gateway prefix path (e.g.
// "/inference/openai") is preserved. Idempotent — a base_url already pointing
// at a supervised relay comes back unchanged.
func (m *Manager) Resolve(ctx context.Context, baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("relay: empty base_url")
	}

	m.mu.Lock()
	for _, p := range m.procs {
		if p.ctx.Err() != nil {
			continue
		}
		if sameOrigin(baseURL, p.baseURL()) {
			m.mu.Unlock()
			return p.Wrap(baseURL), nil
		}
	}
	m.mu.Unlock()

	upstream := UpstreamFor(baseURL)

	m.mu.Lock()
	// A base_url whose origin IS one of our relays (rewritten by an older
	// generation) must forward to that relay's true upstream, not loop.
	if p, ok := m.procs[upstream]; ok && p.ctx.Err() == nil {
		upstream = p.upstream
	}
	m.mu.Unlock()

	relayOrigin, err := m.Ensure(ctx, upstream)
	if err != nil {
		return "", err
	}

	return wrapOrigin(relayOrigin, baseURL), nil
}

// Wrap rewrites a provider base_url to point at this relay, preserving any
// gateway prefix path.
func (p *proc) Wrap(baseURL string) string {
	return wrapOrigin(p.baseURL(), baseURL)
}

func wrapOrigin(relayOrigin, baseURL string) string {
	prefix := ""
	if u, err := url.Parse(baseURL); err == nil {
		prefix = strings.TrimRight(u.Path, "/")
	}
	return strings.TrimRight(relayOrigin, "/") + prefix
}

func sameOrigin(a, b string) bool {
	pa, erra := url.Parse(a)
	pb, errb := url.Parse(b)
	if erra != nil || errb != nil {
		return a == b
	}
	return strings.TrimRight(pa.Scheme+"://"+pa.Host, "/") == strings.TrimRight(pb.Scheme+"://"+pb.Host, "/")
}

// Stop kills every supervised child. Routing falls back to direct afterwards.
func (m *Manager) Stop() {
	m.mu.Lock()
	procs := m.procs
	m.procs = map[string]*proc{}
	m.mu.Unlock()

	for _, p := range procs {
		p.cancel()
	}
	for _, p := range procs {
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *Manager) portFor(upstream string) (int, error) {
	if port, ok := m.ports[upstream]; ok && port != 0 {
		return port, nil
	}
	if fixed := ConfiguredPort(); fixed != 0 {
		m.mu.Lock()
		for u, p := range m.procs {
			if p.port == fixed && u != upstream {
				m.mu.Unlock()
				return 0, fmt.Errorf("relay: fixed port %d already serves upstream %s", fixed, u)
			}
		}
		m.mu.Unlock()
		return fixed, nil
	}
	picked, err := pickFreePort()
	if err != nil {
		return 0, fmt.Errorf("relay: pick free port: %w", err)
	}
	return picked, nil
}

func (m *Manager) startProc(p *proc) error {
	script, err := m.writeScript()
	if err != nil {
		return fmt.Errorf("write relay script: %w", err)
	}

	supCtx, supCancel := context.WithCancel(p.ctx)
	p.mu.Lock()
	p.supCtx, p.supCancel = supCtx, supCancel
	p.mu.Unlock()

	// Child stdio goes to a file, never to pipes: bun re-execs on Windows and
	// an orphaned grandchild holding an inherited pipe stalls `go test` exit
	// ("Test I/O incomplete") even after the direct child is killed. File
	// handles don't block the test binary's exit.
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(script), "relay.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		supCancel()
		return fmt.Errorf("open relay log: %w", err)
	}
	p.mu.Lock()
	p.logFile = logFile
	p.mu.Unlock()

	cmd := exec.CommandContext(p.supCtx, relayBin(), script, "--port", strconv.Itoa(p.port), "--upstream", p.upstream)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.WaitDelay = 3 * time.Second

	// Pdeathsig (Linux) must be installed while pinned to one OS thread, or
	// the Go runtime can migrate the goroutine and kill the child early.
	startErr := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		configureSysProcAttr(cmd)
		startErr <- cmd.Start()
	}()
	if err := <-startErr; err != nil {
		logFile.Close()
		supCancel()
		return fmt.Errorf("spawn %s: %w", relayBin(), err)
	}

	if err := m.waitHealth(p, p.supCtx); err != nil {
		supCancel()
		return err
	}
	return nil
}

// supervise restarts the child while the proc is alive. The gateway's error
// style (flag vs quota) is unaffected — this only keeps the transport up.
func (m *Manager) supervise(p *proc) {
	defer close(p.done)
	for {
		p.mu.Lock()
		cmd := p.cmd
		logFile := p.logFile
		p.mu.Unlock()
		if cmd == nil {
			return
		}
		err := cmd.Wait()
		logFile.Close()
		if p.ctx.Err() != nil {
			return
		}
		m.log.Printf("relay child exited (upstream %s): %v", p.upstream, err)
		time.Sleep(p.backoff)
		if p.ctx.Err() != nil {
			return
		}
		if serr := m.startProc(p); serr != nil {
			p.backoff = min(p.backoff*2, 30*time.Second)
			m.log.Printf("relay respawn failed (upstream %s): %v", p.upstream, serr)
			continue
		}
		p.backoff = time.Second
	}
}

func (m *Manager) waitHealth(p *proc, supCtx context.Context) error {
	client := &http.Client{Timeout: 400 * time.Millisecond}
	deadline := time.Now().Add(healthBudget)
	for time.Now().Before(deadline) {
		if supCtx.Err() != nil || p.ctx.Err() != nil {
			return errors.New("relay: stopped during startup")
		}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", p.port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(healthTick)
	}
	return fmt.Errorf("relay: health check timeout on port %d", p.port)
}

func (m *Manager) writeScript() (string, error) {
	dir := filepath.Join(os.TempDir(), "routerllm-relay")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "bun-relay.ts")
	if err := os.WriteFile(path, []byte(relayScript), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (p *proc) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.port)
}
