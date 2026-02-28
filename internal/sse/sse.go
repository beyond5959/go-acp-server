package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Writer wraps http.ResponseWriter to emit SSE frames.
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewWriter prepares response headers and returns an SSE writer.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("sse: response writer does not support flushing")
	}

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	headers.Set("X-Accel-Buffering", "no")

	return &Writer{w: w, flusher: flusher}, nil
}

// Event writes one SSE event and flushes it.
func (sw *Writer) Event(eventType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sse: marshal payload: %w", err)
	}

	if _, err := fmt.Fprintf(sw.w, "event: %s\n", eventType); err != nil {
		return fmt.Errorf("sse: write event field: %w", err)
	}
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", encoded); err != nil {
		return fmt.Errorf("sse: write data field: %w", err)
	}
	sw.flusher.Flush()
	return nil
}
