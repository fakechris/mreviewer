package llm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
)

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func mustJSON(v any) string { data, _ := json.Marshal(v); return string(data) }

func redactPayload(payload map[string]any) map[string]any {
	data, _ := json.Marshal(payload)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return map[string]any{"redacted": true}
	}
	redacted := redactValue(normalized)
	result, ok := redacted.(map[string]any)
	if !ok || result == nil {
		return map[string]any{"redacted": true}
	}
	return result
}

func redactValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, item := range value {
			lower := normalizeRedactionKey(k)
			switch {
			case strings.Contains(lower, "apikey"), strings.Contains(lower, "authorization"), strings.Contains(lower, "token"), strings.Contains(lower, "cookie"):
				out[k] = "[REDACTED]"
			case lower == "content":
				out[k] = "[OMITTED]"
			default:
				out[k] = redactValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = redactValue(item)
		}
		return out
	case string:
		if len(value) > 256 {
			return value[:256] + "...[truncated]"
		}
		return value
	default:
		return value
	}
}

func redactError(err error) map[string]any {
	if err == nil {
		return nil
	}
	return map[string]any{"message": err.Error(), "timeout": isTimeoutError(err)}
}

func normalizeRedactionKey(key string) string {
	lower := strings.ToLower(strings.TrimSpace(key))
	lower = strings.ReplaceAll(lower, "_", "")
	lower = strings.ReplaceAll(lower, "-", "")
	return lower
}

func redactURL(raw string) string { return strings.TrimRight(raw, "/") }

func nullableString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
