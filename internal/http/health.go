package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mreviewer/mreviewer/internal/logging"
)

// Pinger is satisfied by any value that can verify database connectivity.
// *sql.DB implements this interface, allowing the health handler to be
// tested through the real production code path with a lightweight mock.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// HealthResponse is the JSON body returned by the health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
}

// NewHealthHandler returns an http.HandlerFunc that checks MySQL connectivity
// and returns {"status":"ok"} with HTTP 200 when healthy.
func NewHealthHandler(logger *slog.Logger, p Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := logging.FromContext(ctx, logger)

		if err := p.PingContext(ctx); err != nil {
			l.ErrorContext(ctx, "health check failed: database unreachable", "error", err)
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
