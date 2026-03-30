package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type SnapshotService interface {
	Queue(ctx context.Context) (QueueSnapshot, error)
	Concurrency(ctx context.Context) (ConcurrencySnapshot, error)
	Failures(ctx context.Context) (FailuresSnapshot, error)
}

func NewHandler(service SnapshotService, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/api/queue", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := service.Queue(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /admin/api/concurrency", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := service.Concurrency(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /admin/api/failures", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := service.Failures(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	return Protect(token, mux)
}

func Protect(token string, next http.Handler) http.Handler {
	if strings.TrimSpace(token) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasBearerToken(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasBearerToken(r *http.Request, token string) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == token
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
