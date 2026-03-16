// Package logging provides structured JSON logging via log/slog with
// request-scoped field injection (e.g. request_id).
package logging

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

// NewLogger creates a JSON slog.Logger writing to stdout.
func NewLogger(level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(h)
}

// WithRequestID returns a child context carrying the given request ID.
// Subsequent calls to FromContext will include this value.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, requestID)
}

// RequestIDFromContext extracts the request ID from the context, if set.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// FromContext returns a logger enriched with any request-scoped fields
// stored in ctx (currently request_id).
func FromContext(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if rid := RequestIDFromContext(ctx); rid != "" {
		return logger.With("request_id", rid)
	}
	return logger
}
