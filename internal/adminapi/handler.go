package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const maxListLimit int32 = 1000

type SnapshotService interface {
	Queue(ctx context.Context) (QueueSnapshot, error)
	Concurrency(ctx context.Context) (ConcurrencySnapshot, error)
	Failures(ctx context.Context) (FailuresSnapshot, error)
	Runs(ctx context.Context, filters RunFilters) (RunsSnapshot, error)
	RunDetail(ctx context.Context, runID int64) (RunDetail, error)
	RetryRun(ctx context.Context, runID int64, actor string) (RunDetail, error)
	RerunRun(ctx context.Context, runID int64, actor string) (RunDetail, error)
	CancelRun(ctx context.Context, runID int64, actor string) (RunDetail, error)
	RequeueRun(ctx context.Context, runID int64, actor string) (RunDetail, error)
	IdentityMappings(ctx context.Context, filters IdentityFilters) (IdentityMappingsSnapshot, error)
	ResolveIdentityMapping(ctx context.Context, mappingID int64, platformUsername, platformUserID, actor string) (IdentityMapping, error)
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
	mux.HandleFunc("GET /admin/api/runs", func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseOptionalInt32(r.URL.Query().Get("limit"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		snapshot, err := service.Runs(r.Context(), RunFilters{
			Platform:    strings.TrimSpace(r.URL.Query().Get("platform")),
			Status:      strings.TrimSpace(r.URL.Query().Get("status")),
			ErrorCode:   strings.TrimSpace(r.URL.Query().Get("error_code")),
			ProjectPath: strings.TrimSpace(r.URL.Query().Get("project")),
			HeadSHA:     strings.TrimSpace(r.URL.Query().Get("head_sha")),
			Limit:       limit,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /admin/api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || runID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run id"})
			return
		}
		snapshot, err := service.RunDetail(r.Context(), runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /admin/api/identities", func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseOptionalInt32(r.URL.Query().Get("limit"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		snapshot, err := service.IdentityMappings(r.Context(), IdentityFilters{
			Platform:    strings.TrimSpace(r.URL.Query().Get("platform")),
			Status:      strings.TrimSpace(r.URL.Query().Get("status")),
			ProjectPath: strings.TrimSpace(r.URL.Query().Get("project")),
			Limit:       limit,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST /admin/api/identities/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		mappingID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || mappingID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid identity mapping id"})
			return
		}
		var body struct {
			PlatformUsername string `json:"platform_username"`
			PlatformUserID   string `json:"platform_user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		snapshot, err := service.ResolveIdentityMapping(r.Context(), mappingID, body.PlatformUsername, body.PlatformUserID, "admin")
		if err != nil {
			var actionErr *ActionError
			if errors.As(err, &actionErr) {
				writeJSON(w, actionErr.StatusCode, map[string]string{"error": actionErr.Message})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST /admin/api/runs/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || runID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run id"})
			return
		}
		var snapshot RunDetail
		switch strings.TrimSpace(r.PathValue("action")) {
		case "retry":
			snapshot, err = service.RetryRun(r.Context(), runID, "admin")
		case "rerun":
			snapshot, err = service.RerunRun(r.Context(), runID, "admin")
		case "cancel":
			snapshot, err = service.CancelRun(r.Context(), runID, "admin")
		case "requeue":
			snapshot, err = service.RequeueRun(r.Context(), runID, "admin")
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run action"})
			return
		}
		if err != nil {
			var actionErr *ActionError
			if errors.As(err, &actionErr) {
				writeJSON(w, actionErr.StatusCode, map[string]string{"error": actionErr.Message})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	return Protect(token, mux)
}

func parseOptionalInt32(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, strconv.ErrSyntax
	}
	if value > int64(maxListLimit) {
		return maxListLimit, nil
	}
	return int32(value), nil
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
