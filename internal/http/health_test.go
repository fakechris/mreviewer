package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockPinger implements the Pinger interface for health check testing.
type mockPinger struct {
	pingErr error
}

func (m *mockPinger) PingContext(_ context.Context) error {
	return m.pingErr
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantCode   int
		wantStatus string
	}{
		{
			name:       "healthy",
			pingErr:    nil,
			wantCode:   http.StatusOK,
			wantStatus: "ok",
		},
		{
			name:       "unhealthy db",
			pingErr:    errors.New("connection refused"),
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unhealthy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.NewJSONHandler(discard{}, nil))
			mock := &mockPinger{pingErr: tc.pingErr}

			// Exercise the real production handler path with an
			// interface-backed dependency instead of duplicated test logic.
			handler := NewHealthHandler(logger, mock)

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status code = %d, want %d", rec.Code, tc.wantCode)
			}

			var resp HealthResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", resp.Status, tc.wantStatus)
			}

			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
		})
	}
}

// discard is an io.Writer that discards everything (for silent test logging).
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
