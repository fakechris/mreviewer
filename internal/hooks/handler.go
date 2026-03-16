// Package hooks implements the webhook ingress handler for GitLab webhook events.
// It verifies the X-Gitlab-Token header, rejects malformed payloads, deduplicates
// by delivery key, ignores unsupported events, and audit-logs every verification outcome.
package hooks

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/logging"
)

// maxPayloadBytes limits the size of accepted webhook bodies (1 MB).
const maxPayloadBytes = 1 << 20

// Handler processes incoming GitLab webhook POST requests.
type Handler struct {
	logger *slog.Logger
	db     *sql.DB
	secret string
}

// NewHandler creates a webhook handler. The secret is the expected value of
// the X-Gitlab-Token header. An empty secret causes all requests to be
// rejected with 401.
func NewHandler(logger *slog.Logger, database *sql.DB, secret string) *Handler {
	return &Handler{
		logger: logger,
		db:     database,
		secret: secret,
	}
}

// ServeHTTP handles POST /webhook requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l := logging.FromContext(ctx, h.logger)

	// --- 1. Verify X-Gitlab-Token ---
	token := r.Header.Get("X-Gitlab-Token")
	deliveryKey := h.extractDeliveryKey(r)
	hookSource := detectHookSource(r)
	eventType := r.Header.Get("X-Gitlab-Event")

	if !h.verifyToken(token) {
		reason := "missing_token"
		if token != "" {
			reason = "invalid_token"
		}
		l.WarnContext(ctx, "webhook authentication failed",
			"delivery_key", deliveryKey,
			"rejection_reason", reason,
		)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", reason, eventType)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// --- 2. Read and validate JSON body ---
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes+1))
	if err != nil {
		l.ErrorContext(ctx, "failed to read request body", "error", err)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "body_read_error", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	if len(body) > maxPayloadBytes {
		l.WarnContext(ctx, "payload too large", "delivery_key", deliveryKey)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "payload_too_large", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload too large"})
		return
	}

	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		l.WarnContext(ctx, "malformed JSON payload",
			"delivery_key", deliveryKey,
			"error", err,
		)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "malformed_json", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON"})
		return
	}

	// --- 3. Check for duplicate delivery ---
	if deliveryKey != "" {
		existing, lookupErr := db.New(h.db).GetHookEventByDeliveryKey(ctx, deliveryKey)
		if lookupErr == nil && existing.ID > 0 {
			l.InfoContext(ctx, "duplicate delivery key, acknowledging",
				"delivery_key", deliveryKey,
			)
			h.writeAuditLog(ctx, deliveryKey, hookSource, "deduplicated", "", eventType)
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			l.ErrorContext(ctx, "failed to check delivery key",
				"delivery_key", deliveryKey,
				"error", lookupErr,
			)
			// Continue processing — better to risk a duplicate insert than reject a valid webhook.
		}
	}

	// --- 4. Parse event metadata ---
	parsed := parseWebhookPayload(payload, eventType, hookSource)

	// If the event type is not a merge request event, accept it but do not
	// create any downstream records beyond audit.
	if !isMergeRequestEvent(parsed.EventType) {
		l.InfoContext(ctx, "non-MR event accepted, no action taken",
			"delivery_key", deliveryKey,
			"event_type", parsed.EventType,
		)
		// Still insert a hook_event for audit trail; ignore insert errors for
		// non-MR events since the audit log below is the primary record.
		if insertErr := h.insertHookEventSafe(ctx, l, deliveryKey, hookSource, parsed, payload, "verified", ""); insertErr != nil {
			l.WarnContext(ctx, "failed to insert hook_event for non-MR event",
				"delivery_key", deliveryKey,
				"error", insertErr,
			)
		}
		h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", parsed.EventType)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unsupported_event"})
		return
	}

	// --- 5. Insert hook_event record transactionally ---
	if err := h.insertHookEventSafe(ctx, l, deliveryKey, hookSource, parsed, payload, "verified", ""); err != nil {
		// If the error is a duplicate key violation, treat as dedup.
		if isDuplicateKeyError(err) {
			l.InfoContext(ctx, "duplicate delivery key on insert, acknowledging",
				"delivery_key", deliveryKey,
			)
			h.writeAuditLog(ctx, deliveryKey, hookSource, "deduplicated", "", parsed.EventType)
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
		l.ErrorContext(ctx, "failed to insert hook event",
			"delivery_key", deliveryKey,
			"error", err,
		)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "db_error", parsed.EventType)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// --- 6. Audit the successful receipt ---
	h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", parsed.EventType)

	l.InfoContext(ctx, "webhook accepted",
		"delivery_key", deliveryKey,
		"event_type", parsed.EventType,
		"action", parsed.Action,
		"mr_iid", parsed.MRIID,
		"project_id", parsed.ProjectID,
	)

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// verifyToken performs constant-time comparison of the provided token against
// the configured secret. Returns false if the secret is empty (misconfigured).
func (h *Handler) verifyToken(token string) bool {
	if h.secret == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.secret)) == 1
}

// extractDeliveryKey derives a delivery key from GitLab headers or generates
// a synthetic UUID if none is present. GitLab sends X-Gitlab-Delivery or
// X-Request-ID depending on the hook type.
func (h *Handler) extractDeliveryKey(r *http.Request) string {
	if dk := r.Header.Get("X-Gitlab-Delivery"); dk != "" {
		return dk
	}
	if rid := r.Header.Get("X-Gitlab-Event-UUID"); rid != "" {
		return rid
	}
	// Generate a synthetic delivery key for webhooks without one.
	return "synthetic-" + uuid.New().String()
}

// insertHookEventSafe inserts a hook_events row using direct queries. It
// returns an error if the insert fails (e.g., duplicate key).
func (h *Handler) insertHookEventSafe(
	ctx context.Context,
	l *slog.Logger,
	deliveryKey, hookSource string,
	parsed parsedEvent,
	payload json.RawMessage,
	outcome, rejectionReason string,
) error {
	q := db.New(h.db)

	_, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
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
		l.ErrorContext(ctx, "insert hook_event failed",
			"delivery_key", deliveryKey,
			"error", err,
		)
		return err
	}
	return nil
}

// writeAuditLog writes an audit_logs row for the webhook verification outcome.
// Errors are logged but do not fail the request.
func (h *Handler) writeAuditLog(
	ctx context.Context,
	deliveryKey, hookSource, outcome, rejectionReason, eventType string,
) {
	q := db.New(h.db)
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
		l := logging.FromContext(ctx, h.logger)
		l.ErrorContext(ctx, "failed to write audit log",
			"delivery_key", deliveryKey,
			"error", err,
		)
	}
}

// parsedEvent holds the extracted fields from a webhook payload.
type parsedEvent struct {
	EventType string
	Action    string
	ProjectID int64
	MRIID     int64
	HeadSHA   string
}

// parseWebhookPayload extracts relevant fields from the raw JSON payload.
func parseWebhookPayload(payload json.RawMessage, headerEventType, hookSource string) parsedEvent {
	var raw struct {
		ObjectKind       string `json:"object_kind"`
		EventType        string `json:"event_type"`
		ObjectAttributes struct {
			IID        int64  `json:"iid"`
			Action     string `json:"action"`
			LastCommit struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
		Project struct {
			ID int64 `json:"id"`
		} `json:"project"`
	}

	// Best-effort parse; if it fails, we still have the header event type.
	_ = json.Unmarshal(payload, &raw)

	eventType := headerEventType
	if eventType == "" {
		eventType = raw.EventType
	}
	if eventType == "" {
		eventType = raw.ObjectKind
	}

	return parsedEvent{
		EventType: eventType,
		Action:    raw.ObjectAttributes.Action,
		ProjectID: raw.Project.ID,
		MRIID:     raw.ObjectAttributes.IID,
		HeadSHA:   raw.ObjectAttributes.LastCommit.ID,
	}
}

// isMergeRequestEvent returns true for GitLab merge request event types.
func isMergeRequestEvent(eventType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(eventType))
	switch normalized {
	case "merge_request", "merge request hook":
		return true
	default:
		return false
	}
}

// detectHookSource identifies whether the webhook is from a project, group,
// or system hook based on headers.
func detectHookSource(r *http.Request) string {
	event := r.Header.Get("X-Gitlab-Event")
	if strings.EqualFold(event, "System Hook") {
		return "system"
	}
	// GitLab does not distinguish group from project hooks by header.
	// Default to "project" unless we have other info.
	return "project"
}

// isDuplicateKeyError checks if a MySQL error is a duplicate key violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// MySQL error 1062 is "Duplicate entry"
	return strings.Contains(err.Error(), "Duplicate entry") ||
		strings.Contains(err.Error(), "Error 1062")
}

// toNullInt64 converts a non-zero int64 to sql.NullInt64.
func toNullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
