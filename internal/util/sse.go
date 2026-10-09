package util

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrorFrame is the error object an upstream embeds in a stream it opened with
// HTTP 200.
type ErrorFrame struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ParseErrorFrame reports whether an SSE payload carries an upstream error and
// returns its fields. Gateways that answer 200 and only then fail the model
// call signal it this way instead of with a status code, so a stream carrying
// one is a failure, not an empty completion.
func ParseErrorFrame(payload string) (ErrorFrame, bool) {
	var doc struct {
		Error *ErrorFrame `json:"error"`
	}

	if err := json.Unmarshal([]byte(payload), &doc); err != nil || doc.Error == nil {
		return ErrorFrame{}, false
	}

	return *doc.Error, true
}

func hasChoices(payload string) bool {
	return strings.Contains(payload, `"choices"`)
}

func IterDataLines(r io.Reader, fn func(payload string) bool) (sawDone bool, err error) {
	br := bufio.NewReader(r)
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data: ") {
				payload := trimmed[len("data: "):]
				if payload == "[DONE]" {
					return true, nil
				}
				if !fn(payload) {
					return false, nil
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return false, nil
			}
			return false, rerr
		}
	}
}

// SanitizedErrorFrame replaces an upstream error object on its way to the
// client. The shape is unchanged so client parsers keep working, but the
// upstream's own text — provider names, account ids, model mapping — stays on
// the server, in the logs and in telemetry.
const SanitizedErrorFrame = `{"error":{"code":"upstream_error","message":"the upstream returned an error","type":"upstream_error"}}`

// StreamSSETransform forwards data frames, skipping non-choice frames when
// filterChoices is set, and passes every payload through transform (nil keeps
// the payload verbatim) before it is written, which lets callers rewrite
// response fields per frame. Upstream error frames are replaced by
// SanitizedErrorFrame; the server log keeps the original.
func StreamSSETransform(src io.Reader, dst http.ResponseWriter, filterChoices bool, transform func(payload string) string) error {
	flusher, _ := dst.(http.Flusher)
	sawDone, err := IterDataLines(src, func(payload string) bool {
		write := func(frame string) {
			fmt.Fprintf(dst, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
		}

		if _, isError := ParseErrorFrame(payload); isError {
			write(SanitizedErrorFrame)
			return true
		}
		if filterChoices && !hasChoices(payload) {
			return true
		}
		if transform != nil {
			payload = transform(payload)
		}
		write(payload)
		return true
	})
	if sawDone {
		fmt.Fprintf(dst, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	return err
}

func StreamRawSSE(src io.Reader, dst http.ResponseWriter) error {
	flusher, _ := dst.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
