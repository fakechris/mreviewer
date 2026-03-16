package http

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/mreviewer/mreviewer/internal/logging"
)

// RequestIDMiddleware injects a unique request_id into the request context
// and sets it as a response header. If the incoming request already carries
// an X-Request-ID header, that value is reused.
func RequestIDMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = uuid.New().String()
		}

		ctx := logging.WithRequestID(r.Context(), rid)
		w.Header().Set("X-Request-ID", rid)

		l := logging.FromContext(ctx, logger)
		l.InfoContext(ctx, "request started",
			"method", r.Method,
			"path", r.URL.Path,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
