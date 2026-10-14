package services

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	// maxErrorBodyRead bounds how much of an upstream error body is captured.
	maxErrorBodyRead = 64 << 10
	// maxClientErrorBytes bounds the upstream error text kept for the
	// TruncateErrorBody helper (unit-tested, no live callers since client
	// errors went generic).
	maxClientErrorBytes = 16 << 10
)

// ReadErrorBody drains an error response body for logging and telemetry, capped
// so a broken or hostile upstream cannot feed the proxy an unbounded stream.
func ReadErrorBody(r io.Reader) []byte {
	body, _ := io.ReadAll(io.LimitReader(r, maxErrorBodyRead))

	return body
}

// TruncateErrorBody caps the upstream error text echoed back to a client.
func TruncateErrorBody(body []byte) []byte {
	if len(body) <= maxClientErrorBytes {
		return body
	}

	return body[:maxClientErrorBytes]
}

// redactURLError renders a transport error with the outbound query string
// masked: the provider's Query can carry credentials (`?key=...`), which must
// not reach logs, telemetry, or the client. Scheme, host, and path are kept.
func redactURLError(err error) string {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err.Error()
	}

	return fmt.Sprintf("%s %q: %s", urlErr.Op, redactQuery(urlErr.URL), urlErr.Err)
}

func redactQuery(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i] + "?…redacted"
	}

	return rawURL
}

func respSummary(r *http.Response) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("status=%d ct=%s xrid=%s", r.StatusCode, r.Header.Get("Content-Type"), r.Header.Get("x-request-id"))
}
