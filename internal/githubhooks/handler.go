package githubhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/logging"
)

const maxPayloadBytes = 1 << 20

type RunProcessor interface {
	ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error
}

type Handler struct {
	logger       *slog.Logger
	db           *sql.DB
	secret       string
	runProcessor RunProcessor
}

func NewHandler(logger *slog.Logger, database *sql.DB, secret string, runProcessor RunProcessor) *Handler {
	return &Handler{
		logger:       logger,
		db:           database,
		secret:       secret,
		runProcessor: runProcessor,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx, h.logger)

	deliveryKey := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	signature := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	if len(body) > maxPayloadBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload too large"})
		return
	}
	if !h.verifySignature(body, signature) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON"})
		return
	}
	if eventType != "pull_request" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unsupported_event"})
		return
	}
	normalized, err := NormalizeWebhook(payload, eventType)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to normalize payload"})
		return
	}

	err = db.RunTx(ctx, h.db, func(ctx context.Context, q *db.Queries) error {
		if deliveryKey != "" {
			if _, lookupErr := q.GetHookEventByDeliveryKey(ctx, deliveryKey); lookupErr == nil {
				return nil
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return lookupErr
			}
		}

		res, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
			DeliveryKey:         deliveryKey,
			HookSource:          "github",
			EventType:           normalized.EventType,
			ProjectID:           sql.NullInt64{Int64: normalized.ProjectID, Valid: normalized.ProjectID > 0},
			MrIid:               sql.NullInt64{Int64: normalized.MRIID, Valid: normalized.MRIID > 0},
			Action:              normalized.Action,
			HeadSha:             normalized.HeadSHA,
			Payload:             payload,
			VerificationOutcome: "verified",
		})
		if err != nil {
			return err
		}
		hookEventID, _ := res.LastInsertId()
		if h.runProcessor == nil {
			return nil
		}
		return h.runProcessor.ProcessEventWithQuerier(ctx, q, normalized, hookEventID)
	})
	if err != nil {
		if db.IsDuplicateKeyError(err) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		logger.ErrorContext(ctx, "github webhook processing failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *Handler) verifySignature(body []byte, signature string) bool {
	secret := strings.TrimSpace(h.secret)
	if secret == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
