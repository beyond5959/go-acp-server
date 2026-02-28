package observability

import (
	"log/slog"
	"os"
)

// NewJSONLogger builds a stderr JSON logger.
func NewJSONLogger(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}
