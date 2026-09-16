package util

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func hasChoices(payload string) bool {
	return strings.Contains(payload, `"choices"`)
}

func hasError(payload string) bool {
	return strings.Contains(payload, `"error"`)
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

func StreamSSE(src io.Reader, dst http.ResponseWriter, filterChoices bool) error {
	return StreamSSETransform(src, dst, filterChoices, nil)
}

// StreamSSETransform forwards data frames exactly like StreamSSE and passes
// every payload through transform (nil keeps the payload verbatim) before it is
// written, which lets callers rewrite response fields per frame.
func StreamSSETransform(src io.Reader, dst http.ResponseWriter, filterChoices bool, transform func(payload string) string) error {
	flusher, _ := dst.(http.Flusher)
	sawDone, err := IterDataLines(src, func(payload string) bool {
		if filterChoices && !hasChoices(payload) && !hasError(payload) {
			return true
		}
		if transform != nil {
			payload = transform(payload)
		}
		fmt.Fprintf(dst, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
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
