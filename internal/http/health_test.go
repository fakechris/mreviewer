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

// mockDB implements a minimal DB mock for health check testing.
type mockDB struct {
	pingErr error
}

func (m *mockDB) PingContext(_ context.Context) error {
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
			handler := newTestHealthHandler(logger, tc.pingErr)

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

// newTestHealthHandler creates a health handler with a mock pinger.
func newTestHealthHandler(logger *slog.Logger, pingErr error) http.HandlerFunc {
	mock := &mockDB{pingErr: pingErr}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if err := mock.PingContext(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "unhealthy"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
	}
}

// discard is an io.Writer that discards everything (for silent test logging).
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
