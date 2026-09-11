package services

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"routerllm/internal/keys"
	"routerllm/internal/provider"
)

// stubPublicHost replaces the package host check for one test and restores the
// original afterwards.
func stubPublicHost(t *testing.T, allow func(host string) bool) {
	t.Helper()

	previous := isPublicHost
	isPublicHost = allow
	t.Cleanup(func() { isPublicHost = previous })
}

// headerRecordingTransport records the headers of every request it forwards.
type headerRecordingTransport struct {
	base http.RoundTripper

	mu      sync.Mutex
	headers []http.Header
}

func (rt *headerRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.headers = append(rt.headers, req.Header.Clone())
	rt.mu.Unlock()

	return rt.base.RoundTrip(req)
}

func (rt *headerRecordingTransport) recorded() []http.Header {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return append([]http.Header(nil), rt.headers...)
}

func newMediaTestProxy(client *http.Client) *Proxy {
	if client == nil {
		client = &http.Client{}
	}

	return NewProxy(provider.NewRegistry(nil, nil, time.Minute), client, log.New(io.Discard, "", 0), false, false, false, false, nil, "")
}

func TestMediaResolverAbsoluteURLSendsNoProviderAuth(t *testing.T) {
	stubPublicHost(t, func(string) bool { return true })

	payload := []byte("image-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(payload)
	}))
	defer upstream.Close()

	recorder := &headerRecordingTransport{base: http.DefaultTransport}
	proxy := newMediaTestProxy(&http.Client{Transport: recorder})
	pv := &provider.Provider{
		Name:     "fake",
		BaseURL:  upstream.URL,
		AuthMode: "bearer",
		Headers:  map[string]string{"x-provider-header": "provider-secret-header"},
		Keys:     keys.New([]string{"provider-secret"}, time.Minute),
	}

	data, mediaType, err := proxy.mediaResolver(pv)(upstream.URL + "/image.png")
	if err != nil {
		t.Fatalf("resolve absolute URL: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("data = %q, want %q", data, payload)
	}
	if mediaType != "image/png" {
		t.Fatalf("mediaType = %q, want image/png", mediaType)
	}

	recorded := recorder.recorded()
	if len(recorded) != 1 {
		t.Fatalf("requests = %d, want 1", len(recorded))
	}
	for _, header := range recorded {
		if got := header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization leaked to client URL: %q", got)
		}
		if got := header.Get("x-api-key"); got != "" {
			t.Fatalf("x-api-key leaked to client URL: %q", got)
		}
		if got := header.Get("x-provider-header"); got != "" {
			t.Fatalf("provider header leaked to client URL: %q", got)
		}
	}
}

func TestMediaResolverFileReferenceSendsProviderAuth(t *testing.T) {
	stubPublicHost(t, func(string) bool { return true })

	var (
		mu      sync.Mutex
		gotPath string
		gotAuth string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()

		w.Header().Set("Content-Type", "image/jpeg")
		io.WriteString(w, "file-bytes")
	}))
	defer upstream.Close()

	proxy := newMediaTestProxy(nil)
	pv := &provider.Provider{
		Name:     "fake",
		BaseURL:  upstream.URL,
		AuthMode: "bearer",
		Keys:     keys.New([]string{"provider-secret"}, time.Minute),
	}

	data, mediaType, err := proxy.mediaResolver(pv)("file-123")
	if err != nil {
		t.Fatalf("resolve file reference: %v", err)
	}
	if string(data) != "file-bytes" {
		t.Fatalf("data = %q, want file-bytes", data)
	}
	if mediaType != "image/jpeg" {
		t.Fatalf("mediaType = %q, want image/jpeg", mediaType)
	}

	mu.Lock()
	path, auth := gotPath, gotAuth
	mu.Unlock()

	if path != "/v1/files/file-123/content" {
		t.Fatalf("path = %q, want /v1/files/file-123/content", path)
	}
	if auth != "Bearer provider-secret" {
		t.Fatalf("Authorization = %q, want the provider key", auth)
	}
}

func TestMediaFetchRejectsRedirectToBlockedHost(t *testing.T) {
	stubPublicHost(t, func(host string) bool { return host != "blocked.invalid" })

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://blocked.invalid/next", http.StatusFound)
	}))
	defer upstream.Close()

	proxy := newMediaTestProxy(nil)

	_, _, err := proxy.mediaResolverNoAuth(&provider.Provider{Name: "fake"})(upstream.URL + "/redirect")
	if err == nil {
		t.Fatal("redirect to a blocked host must fail")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Fatalf("error = %v, want public-host rejection", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (redirect target must not be fetched)", got)
	}
}

func TestMediaResolverCapsReferencesPerRequest(t *testing.T) {
	stubPublicHost(t, func(string) bool { return true })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tiny")
	}))
	defer upstream.Close()

	proxy := newMediaTestProxy(nil)
	resolve := proxy.mediaResolver(&provider.Provider{Name: "fake"})

	for i := 0; i < maxMediaRefsPerRequest; i++ {
		if _, _, err := resolve(upstream.URL + "/media"); err != nil {
			t.Fatalf("reference %d: %v", i+1, err)
		}
	}

	_, _, err := resolve(upstream.URL + "/media")
	if err == nil || !strings.Contains(err.Error(), "media budget exceeded") {
		t.Fatalf("error = %v, want media budget exceeded", err)
	}
}

func TestMediaResolverCapsTotalBytesPerRequest(t *testing.T) {
	stubPublicHost(t, func(string) bool { return true })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(maxResolvedMediaSize))

		buf := make([]byte, 64<<10)
		for sent := 0; sent < maxResolvedMediaSize; sent += len(buf) {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	proxy := newMediaTestProxy(nil)
	resolve := proxy.mediaResolver(&provider.Provider{Name: "fake"})

	var exceeded bool
	for i := 0; i < maxMediaRefsPerRequest; i++ {
		if _, _, err := resolve(upstream.URL + "/big"); err != nil {
			if !strings.Contains(err.Error(), "media budget exceeded") {
				t.Fatalf("reference %d: unexpected error %v", i+1, err)
			}
			exceeded = true
			break
		}
	}
	if !exceeded {
		t.Fatalf("resolving %d references of %d bytes never hit the %d-byte budget", maxMediaRefsPerRequest, maxResolvedMediaSize, maxMediaBytesPerRequest)
	}
}
