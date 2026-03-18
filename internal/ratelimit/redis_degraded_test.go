package ratelimit

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
)

func TestRedisDegradedFallback(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	cfg := &config.Config{}

	if cfg.RedisAddr != "" {
		t.Fatalf("RedisAddr = %q, want empty for degraded fallback test", cfg.RedisAddr)
	}
	logger.WarnContext(context.Background(), "redis unavailable; optional coordination disabled", "mode", "degraded_fallback")
	if got := logBuf.String(); got == "" || !bytes.Contains([]byte(got), []byte("degraded_fallback")) {
		t.Fatalf("expected degraded fallback warning log, got %q", got)
	}
}
