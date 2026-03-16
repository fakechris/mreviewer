package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestJSONLogging(t *testing.T) {
	tests := []struct {
		name          string
		requestID     string
		message       string
		level         slog.Level
		wantRequestID bool
	}{
		{
			name:          "info log without request_id",
			requestID:     "",
			message:       "hello world",
			level:         slog.LevelInfo,
			wantRequestID: false,
		},
		{
			name:          "info log with request_id",
			requestID:     "req-abc-123",
			message:       "processing request",
			level:         slog.LevelInfo,
			wantRequestID: true,
		},
		{
			name:          "error log with request_id",
			requestID:     "req-err-456",
			message:       "something failed",
			level:         slog.LevelError,
			wantRequestID: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			logger := slog.New(h)

			ctx := context.Background()
			if tc.requestID != "" {
				ctx = WithRequestID(ctx, tc.requestID)
			}

			l := FromContext(ctx, logger)
			l.Log(ctx, tc.level, tc.message)

			// Parse the JSON output.
			var entry map[string]any
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("log output is not valid JSON: %v\nraw: %s", err, buf.String())
			}

			// Verify required fields.
			if _, ok := entry["time"]; !ok {
				t.Error("missing 'time' field in log entry")
			}
			if _, ok := entry["level"]; !ok {
				t.Error("missing 'level' field in log entry")
			}
			if msg, ok := entry["msg"]; !ok {
				t.Error("missing 'msg' field in log entry")
			} else if msg != tc.message {
				t.Errorf("msg = %q, want %q", msg, tc.message)
			}

			// Verify request_id presence/absence.
			rid, hasRID := entry["request_id"]
			if tc.wantRequestID {
				if !hasRID {
					t.Error("expected 'request_id' field but not found")
				} else if rid != tc.requestID {
					t.Errorf("request_id = %q, want %q", rid, tc.requestID)
				}
			} else {
				if hasRID {
					t.Errorf("unexpected 'request_id' field: %v", rid)
				}
			}
		})
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := context.Background()

	// No request_id initially.
	if got := RequestIDFromContext(ctx); got != "" {
		t.Errorf("expected empty request_id, got %q", got)
	}

	// Set and retrieve.
	ctx = WithRequestID(ctx, "test-123")
	if got := RequestIDFromContext(ctx); got != "test-123" {
		t.Errorf("request_id = %q, want %q", got, "test-123")
	}
}
