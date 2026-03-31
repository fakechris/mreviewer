package githubhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
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
	newStore     func(db.DBTX) db.Store
}

type HandlerOption func(*Handler)

func WithHandlerStoreFactory(fn func(db.DBTX) db.Store) HandlerOption {
	return func(h *Handler) {
		if fn != nil {
			h.newStore = fn
		}
	}
}

func NewHandler(logger *slog.Logger, database *sql.DB, secret string, runProcessor RunProcessor, opts ...HandlerOption) *Handler {
	h := &Handler{
		logger:       logger,
		db:           database,
		secret:       secret,
		runProcessor: runProcessor,
		newStore:     func(conn db.DBTX) db.Store { return db.New(conn) },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l := logging.FromContext(ctx, h.logger)
	deliveryKey := h.extractDeliveryKey(r)
	eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	hookSource := "project"

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes+1))
	if err != nil {
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "body_read_error", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	if len(body) > maxPayloadBytes {
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "payload_too_large", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload too large"})
		return
	}
	if !h.verifySignature(r.Header.Get("X-Hub-Signature-256"), body) {
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "invalid_signature", eventType)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "malformed_json", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON"})
		return
	}
	if deliveryKey != "" {
		existing, lookupErr := h.newStore(h.db).GetHookEventByDeliveryKey(ctx, deliveryKey)
		if lookupErr == nil && existing.ID > 0 {
			h.writeAuditLog(ctx, deliveryKey, hookSource, "deduplicated", "", eventType)
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		if lookupErr != nil && lookupErr != sql.ErrNoRows {
			l.WarnContext(ctx, "failed to check github delivery key", "delivery_key", deliveryKey, "error", lookupErr)
		}
	}

	if eventType != "pull_request" {
		parsed := parsedEvent{EventType: eventType}
		if _, err := h.insertHookEvent(ctx, h.newStore(h.db), deliveryKey, hookSource, parsed, payload, "verified", ""); err != nil && !db.IsDuplicateKeyError(err) {
			l.WarnContext(ctx, "failed to insert github hook event", "delivery_key", deliveryKey, "error", err)
		}
		h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", eventType)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unsupported_event"})
		return
	}

	normalized, err := NormalizeWebhook(payload, eventType)
	if err != nil {
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "normalization_error", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to normalize payload"})
		return
	}
	parsed := parsedEvent{
		EventType: normalized.EventType,
		Action:    normalized.Action,
		ProjectID: normalized.ProjectID,
		MRIID:     normalized.MRIID,
		HeadSHA:   normalized.HeadSHA,
	}

	err = db.RunTxWithStore(ctx, h.db, h.newStore, func(ctx context.Context, s db.Store) error {
		hookEventID, err := h.insertHookEvent(ctx, s, deliveryKey, hookSource, parsed, payload, "verified", "")
		if err != nil {
			return err
		}
		if h.runProcessor == nil {
			return nil
		}
		return h.runProcessor.ProcessEventWithQuerier(ctx, s, normalized, hookEventID)
	})
	if err != nil {
		if db.IsDuplicateKeyError(err) {
			h.writeAuditLog(ctx, deliveryKey, hookSource, "deduplicated", "", eventType)
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "db_error", eventType)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", eventType)
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *Handler) verifySignature(signature string, body []byte) bool {
	if strings.TrimSpace(h.secret) == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

func (h *Handler) extractDeliveryKey(r *http.Request) string {
	if dk := r.Header.Get("X-GitHub-Delivery"); dk != "" {
		return dk
	}
	return "synthetic-" + uuid.New().String()
}

type parsedEvent struct {
	EventType string
	Action    string
	ProjectID int64
	MRIID     int64
	HeadSHA   string
}

func (h *Handler) insertHookEvent(
	ctx context.Context,
	q db.Querier,
	deliveryKey, hookSource string,
	parsed parsedEvent,
	payload json.RawMessage,
	outcome, rejectionReason string,
) (int64, error) {
	result, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
		DeliveryKey:         deliveryKey,
		HookSource:          hookSource,
		EventType:           parsed.EventType,
		GitlabInstanceID:    sql.NullInt64{},
		ProjectID:           toNullInt64(parsed.ProjectID),
		MrIid:               toNullInt64(parsed.MRIID),
		Action:              parsed.Action,
		HeadSha:             parsed.HeadSHA,
		Payload:             payload,
		VerificationOutcome: outcome,
		RejectionReason:     rejectionReason,
	})
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert hook_event: last insert id: %w", err)
	}
	return id, nil
}

func (h *Handler) writeAuditLog(ctx context.Context, deliveryKey, hookSource, outcome, rejectionReason, eventType string) {
	q := h.newStore(h.db)
	_, err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		EntityType:          "webhook",
		EntityID:            0,
		Action:              "webhook_received",
		Actor:               "system",
		Detail:              json.RawMessage(fmt.Sprintf(`{"event_type":%q}`, eventType)),
		DeliveryKey:         deliveryKey,
		HookSource:          hookSource,
		VerificationOutcome: outcome,
		RejectionReason:     rejectionReason,
	})
	if err != nil {
		logging.FromContext(ctx, h.logger).ErrorContext(ctx, "failed to write github audit log", "delivery_key", deliveryKey, "error", err)
	}
}

func toNullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
