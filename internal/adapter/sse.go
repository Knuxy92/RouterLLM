package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sseWriter writes SSE frames to a destination, flushing after every frame
// when the destination supports it.
type sseWriter struct {
	dst     io.Writer
	flusher http.Flusher
}

func newSSEWriter(dst io.Writer) sseWriter {
	flusher, _ := dst.(http.Flusher)
	return sseWriter{dst: dst, flusher: flusher}
}

func (w sseWriter) frame(payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w.dst, "data: %s\n\n", data)
	w.flush()
}

func (w sseWriter) event(name string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w.dst, "event: %s\ndata: %s\n\n", name, data)
	w.flush()
}

func (w sseWriter) done() {
	fmt.Fprintf(w.dst, "data: [DONE]\n\n")
	w.flush()
}

func (w sseWriter) flush() {
	if w.flusher != nil {
		w.flusher.Flush()
	}
}
