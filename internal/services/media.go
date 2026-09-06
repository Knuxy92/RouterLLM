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

const maxResolvedMediaSize = 20 << 20

func (p *Proxy) mediaResolver(pv *provider.Provider) adapter.MediaResolver {
	return func(reference string) ([]byte, string, error) {
		if parsed, err := url.Parse(reference); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return p.fetchMedia(pv, parsed.String())
		}
		if reference == "" || strings.ContainsAny(reference, "/\\") {
			return nil, "", fmt.Errorf("unsupported media reference")
		}
		return p.fetchMedia(pv, pv.BaseURL+"/v1/files/"+url.PathEscape(reference)+"/content")
	}
}

// mediaResolverNoAuth is like mediaResolver but only accepts absolute public
// http(s) URLs and never attaches provider credentials — used for google-style
// providers so the API key is never sent to third-party media hosts.
func (p *Proxy) mediaResolverNoAuth(pv *provider.Provider) adapter.MediaResolver {
	return func(reference string) ([]byte, string, error) {
		parsed, err := url.Parse(reference)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, "", fmt.Errorf("unsupported media reference")
		}
		return p.fetchMediaNoAuth(parsed.String())
	}
}

func (p *Proxy) fetchMedia(pv *provider.Provider, rawURL string) ([]byte, string, error) {
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

	return p.doMedia(req)
}

// fetchMediaNoAuth fetches a public URL without provider credentials.
func (p *Proxy) fetchMediaNoAuth(rawURL string) ([]byte, string, error) {
	req, err := newMediaRequest(rawURL)
	if err != nil {
		return nil, "", err
	}

	return p.doMedia(req)
}

func (p *Proxy) doMedia(req *http.Request) ([]byte, string, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetch media returned status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxResolvedMediaSize {
		return nil, "", fmt.Errorf("media exceeds %d bytes", maxResolvedMediaSize)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResolvedMediaSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read media: %w", err)
	}
	if len(data) > maxResolvedMediaSize {
		return nil, "", fmt.Errorf("media exceeds %d bytes", maxResolvedMediaSize)
	}
	mediaType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return data, mediaType, nil
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

func isPublicHost(host string) bool {
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
