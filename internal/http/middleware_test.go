package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mreviewer/mreviewer/internal/logging"
)

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(discard{}, nil))

	var capturedRID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRID = logging.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := RequestIDMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedRID == "" {
		t.Error("expected request_id to be set in context")
	}

	respRID := rec.Header().Get("X-Request-ID")
	if respRID == "" {
		t.Error("expected X-Request-ID response header")
	}
	if respRID != capturedRID {
		t.Errorf("response header X-Request-ID = %q, context request_id = %q", respRID, capturedRID)
	}
}

func TestRequestIDMiddleware_ReusesExisting(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(discard{}, nil))

	var capturedRID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRID = logging.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := RequestIDMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "existing-id-42")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedRID != "existing-id-42" {
		t.Errorf("request_id = %q, want %q", capturedRID, "existing-id-42")
	}

	respRID := rec.Header().Get("X-Request-ID")
	if respRID != "existing-id-42" {
		t.Errorf("response X-Request-ID = %q, want %q", respRID, "existing-id-42")
	}
}
