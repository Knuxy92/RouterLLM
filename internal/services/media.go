package services

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"routerllm/internal/adapter"
	"routerllm/internal/provider"
)

const (
	maxResolvedMediaSize = 20 << 20

	maxMediaRedirects       = 10
	maxMediaRefsPerRequest  = 8
	maxMediaBytesPerRequest = 50 << 20
)

// mediaBudget tracks what one request may still resolve: the number of
// references and the total fetched bytes. Each resolver closure owns one
// budget, so both caps are per translateRoute call.
type mediaBudget struct {
	refs  int
	bytes int
}

func newMediaBudget() *mediaBudget {
	return &mediaBudget{}
}

// reserveRef claims one reference slot.
func (b *mediaBudget) reserveRef() error {
	if b.refs >= maxMediaRefsPerRequest {
		return fmt.Errorf("media budget exceeded: more than %d references per request", maxMediaRefsPerRequest)
	}
	b.refs++

	return nil
}

// fetchLimit is the largest body allowed for one fetch: the smaller of the
// single-media cap and what remains of the per-request byte budget.
func (b *mediaBudget) fetchLimit() int {
	remaining := maxMediaBytesPerRequest - b.bytes
	if remaining > maxResolvedMediaSize {
		return maxResolvedMediaSize
	}

	return remaining
}

func (b *mediaBudget) consume(n int) {
	b.bytes += n
}

// mediaResolver resolves a client media reference for an anthropic-style
// route. Absolute public http(s) URLs are fetched without provider credentials
// — a client-supplied URL must never see the provider key. Only the
// provider-relative file reference is fetched with the provider's auth.
func (p *Proxy) mediaResolver(pv *provider.Provider) adapter.MediaResolver {
	budget := newMediaBudget()

	return func(reference string) ([]byte, string, error) {
		parsed, err := url.Parse(reference)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			if err := budget.reserveRef(); err != nil {
				return nil, "", err
			}

			return p.fetchMediaNoAuth(parsed.String(), budget)
		}

		if reference == "" || strings.ContainsAny(reference, "/\\") {
			return nil, "", fmt.Errorf("unsupported media reference")
		}
		if err := budget.reserveRef(); err != nil {
			return nil, "", err
		}

		return p.fetchMedia(pv, pv.BaseURL+"/v1/files/"+url.PathEscape(reference)+"/content", budget)
	}
}

// mediaResolverNoAuth is like mediaResolver but only accepts absolute public
// http(s) URLs and never attaches provider credentials — used for google-style
// providers so the API key is never sent to third-party media hosts.
func (p *Proxy) mediaResolverNoAuth(pv *provider.Provider) adapter.MediaResolver {
	budget := newMediaBudget()

	return func(reference string) ([]byte, string, error) {
		parsed, err := url.Parse(reference)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, "", fmt.Errorf("unsupported media reference")
		}

		if err := budget.reserveRef(); err != nil {
			return nil, "", err
		}

		return p.fetchMediaNoAuth(parsed.String(), budget)
	}
}

// fetchMedia fetches a provider-relative media URL with the provider's headers
// and credential.
func (p *Proxy) fetchMedia(pv *provider.Provider, rawURL string, budget *mediaBudget) ([]byte, string, error) {
	req, err := newMediaRequest(rawURL)
	if err != nil {
		return nil, "", err
	}

	for key, value := range pv.Headers {
		req.Header.Set(key, value)
	}
	key := pv.Keys.LiveKey()
	switch pv.AuthMode {
	case "both":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
	case "x-api-key":
		req.Header.Set("x-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}

	return p.doMedia(req, budget)
}

// fetchMediaNoAuth fetches a public URL without provider credentials.
func (p *Proxy) fetchMediaNoAuth(rawURL string, budget *mediaBudget) ([]byte, string, error) {
	req, err := newMediaRequest(rawURL)
	if err != nil {
		return nil, "", err
	}

	return p.doMedia(req, budget)
}

func (p *Proxy) doMedia(req *http.Request, budget *mediaBudget) ([]byte, string, error) {
	limit := budget.fetchLimit()
	if limit <= 0 {
		return nil, "", fmt.Errorf("media budget exceeded: %d bytes already fetched", maxMediaBytesPerRequest)
	}

	resp, err := p.mediaClient().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetch media returned status %d", resp.StatusCode)
	}

	if resp.ContentLength > int64(limit) {
		return nil, "", fmt.Errorf("media budget exceeded: response exceeds the %d-byte allowance", limit)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, "", fmt.Errorf("read media: %w", err)
	}

	if len(data) > limit {
		return nil, "", fmt.Errorf("media budget exceeded: response exceeds the %d-byte allowance", limit)
	}
	budget.consume(len(data))

	mediaType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}

	return data, mediaType, nil
}

// mediaClient builds the client used for media fetches. It reuses the shared
// transport but re-validates every redirect hop with the same public-host check
// that guards the initial URL; p.client itself serves upstream API calls and
// must not carry a CheckRedirect.
func (p *Proxy) mediaClient() *http.Client {
	return &http.Client{
		Transport: p.client.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxMediaRedirects {
				return fmt.Errorf("media fetch stopped after %d redirects", maxMediaRedirects)
			}
			if !isPublicHost(req.URL.Hostname()) {
				return fmt.Errorf("media redirect target is not public")
			}

			return nil
		},
	}
}

func newMediaRequest(rawURL string) (*http.Request, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("media URL must use http or https")
	}
	if parsed.Hostname() == "" || !isPublicHost(parsed.Hostname()) {
		return nil, fmt.Errorf("media URL target is not public")
	}

	return http.NewRequest(http.MethodGet, parsed.String(), nil)
}

// isPublicHost is a var so tests can stub the network lookup.
var isPublicHost = func(host string) bool {
	ip := net.ParseIP(host)
	if ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
			return false
		}
	}

	return true
}
