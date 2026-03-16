package http

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mreviewer/mreviewer/internal/logging"
)

// HealthResponse is the JSON body returned by the health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
}

// NewHealthHandler returns an http.HandlerFunc that checks MySQL connectivity
// and returns {"status":"ok"} with HTTP 200 when healthy.
func NewHealthHandler(logger *slog.Logger, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := logging.FromContext(ctx, logger)

		if err := db.PingContext(ctx); err != nil {
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
