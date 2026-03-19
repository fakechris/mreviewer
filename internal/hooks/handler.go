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
	logger           *slog.Logger
	db               *sql.DB
	secret           string
	runProcessor     RunProcessor
	commandProcessor CommandProcessor
}

// RunProcessor creates or cancels review runs for normalized MR events.
// Implementations can optionally participate in an existing transaction by
// using the provided querier.
type RunProcessor interface {
	ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev NormalizedEvent, hookEventID int64) error
}

// CommandProcessor handles /ai-review commands from note webhook events and
// must support execution inside a caller-managed transaction so hook-event
// persistence and command side effects remain atomic across retries.
type CommandProcessor interface {
	Execute(ctx context.Context, noteEvent NormalizedNoteEvent, cmd interface{}) error
	ExecuteWithQuerier(ctx context.Context, q *db.Queries, noteEvent NormalizedNoteEvent, cmd interface{}) error
}

// NewHandler creates a webhook handler. The secret is the expected value of
// the X-Gitlab-Token header. An empty secret causes all requests to be
// rejected with 401.
func NewHandler(logger *slog.Logger, database *sql.DB, secret string, runProcessor RunProcessor) *Handler {
	return &Handler{
		logger:       logger,
		db:           database,
		secret:       secret,
		runProcessor: runProcessor,
	}
}

// SetCommandProcessor sets an optional command processor for /ai-review
// note commands. This is separated from the constructor to avoid a circular
// dependency.
func (h *Handler) SetCommandProcessor(cp CommandProcessor) {
	if cp == nil {
		h.commandProcessor = nil
		return
	}
	h.commandProcessor = cp
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

	// Project and group MR webhooks share the same X-Gitlab-Event header.
	// Refine the request-level hint with payload markers before dedupe/audit.
	hookSource = inferWebhookSource(payload, hookSource)

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

	// --- 4a. Check if this is a note/comment event with a command ---
	isNoteEvent := IsNoteEventType(eventType)
	if !isNoteEvent && IsSystemHookHeader(eventType) {
		isNoteEvent = IsNotePayload(payload)
	}

	if isNoteEvent && IsMergeRequestNotePayload(payload) {
		h.handleNoteEvent(ctx, w, l, deliveryKey, hookSource, eventType, payload)
		return
	}

	// --- 4b. Determine if this is a merge request event ---
	// For system hooks, the header is always "System Hook" regardless of the
	// actual event kind, so we must inspect the payload's object_kind.
	isMREvent := IsMergeRequestEventType(eventType)
	if !isMREvent && IsSystemHookHeader(eventType) {
		isMREvent = IsMergeRequestPayload(payload)
	}

	if !isMREvent {
		// Not an MR event: accept for audit but do not create downstream records.
		parsed := parseWebhookPayload(payload, eventType, hookSource)
		l.InfoContext(ctx, "non-MR event accepted, no action taken",
			"delivery_key", deliveryKey,
			"event_type", parsed.EventType,
		)
		if _, insertErr := h.insertHookEvent(ctx, db.New(h.db), deliveryKey, hookSource, parsed, payload, "verified", ""); insertErr != nil {
			l.WarnContext(ctx, "failed to insert hook_event for non-MR event",
				"delivery_key", deliveryKey,
				"error", insertErr,
			)
		}
		h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", parsed.EventType)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unsupported_event"})
		return
	}

	// --- 5. Normalize the MR event ---
	normalized, normErr := NormalizeWebhook(payload, eventType, hookSource)
	if normErr != nil {
		l.ErrorContext(ctx, "failed to normalize webhook payload",
			"delivery_key", deliveryKey,
			"error", normErr,
		)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "normalization_error", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to normalize payload"})
		return
	}
	hookSource = normalized.HookSource

	// Build parsed event from normalized data for hook_event insertion.
	parsed := parsedEvent{
		EventType: normalized.EventType,
		Action:    normalized.Action,
		ProjectID: normalized.ProjectID,
		MRIID:     normalized.MRIID,
		HeadSHA:   normalized.HeadSHA,
	}

	// --- 6. Insert hook_event record and process run lifecycle atomically ---
	err = db.RunTx(ctx, h.db, func(ctx context.Context, q *db.Queries) error {
		hookEventID, err := h.insertHookEvent(ctx, q, deliveryKey, hookSource, parsed, payload, "verified", "")
		if err != nil {
			return err
		}

		if h.runProcessor == nil {
			return nil
		}

		return h.runProcessor.ProcessEventWithQuerier(ctx, q, normalized, hookEventID)
	})
	if err != nil {
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

	// --- 7. Audit the successful receipt ---
	h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", parsed.EventType)

	l.InfoContext(ctx, "webhook accepted",
		"delivery_key", deliveryKey,
		"event_type", normalized.EventType,
		"action", normalized.Action,
		"mr_iid", normalized.MRIID,
		"project_id", normalized.ProjectID,
		"head_sha", normalized.HeadSHA,
		"head_sha_deferred", normalized.HeadSHADeferred,
		"is_draft", normalized.IsDraft,
		"hook_source", normalized.HookSource,
		"trigger_type", normalized.TriggerType,
		"idempotency_key", normalized.IdempotencyKey,
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
// a synthetic UUID if none is present. Prefer X-Gitlab-Webhook-UUID for
// GitLab 16.4+, then preserve X-Gitlab-Delivery and the legacy
// X-Gitlab-Event-UUID fallback for older/self-managed installations.
func (h *Handler) extractDeliveryKey(r *http.Request) string {
	if dk := r.Header.Get("X-Gitlab-Webhook-UUID"); dk != "" {
		return dk
	}
	if dk := r.Header.Get("X-Gitlab-Delivery"); dk != "" {
		return dk
	}
	if rid := r.Header.Get("X-Gitlab-Event-UUID"); rid != "" {
		return rid
	}
	// Generate a synthetic delivery key for webhooks without one.
	return "synthetic-" + uuid.New().String()
}

// insertHookEvent inserts a hook_events row and returns its ID.
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

	hookEventID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert hook_event: last insert id: %w", err)
	}

	return hookEventID, nil
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

// handleNoteEvent processes note/comment webhook events. If the note body
// starts with /ai-review, it routes to the command processor. Hook-event
// persistence and command execution are wrapped in a single transaction so
// that a retried delivery after a partial failure can be safely replayed:
// if the command fails, the hook_event is rolled back and the retry will
// not be deduplicated.
func (h *Handler) handleNoteEvent(
	ctx context.Context,
	w http.ResponseWriter,
	l *slog.Logger,
	deliveryKey, hookSource, eventType string,
	payload json.RawMessage,
) {
	// Parse the note event.
	noteEvent, err := NormalizeNoteWebhook(payload, eventType, hookSource)
	if err != nil {
		l.ErrorContext(ctx, "failed to normalize note webhook",
			"delivery_key", deliveryKey,
			"error", err,
		)
		h.writeAuditLog(ctx, deliveryKey, hookSource, "rejected", "note_normalization_error", eventType)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to normalize note payload"})
		return
	}

	// Attach the delivery key so command-triggered idempotency keys are stable
	// across replayed deliveries of the same note event.
	noteEvent.DeliveryKey = deliveryKey

	parsed := parsedEvent{
		EventType: eventType,
		Action:    "note",
		ProjectID: noteEvent.ProjectID,
		MRIID:     noteEvent.MRIID,
		HeadSHA:   noteEvent.HeadSHA,
	}

	// Check if the note body contains a /ai-review command.
	isCmd := isCommandNote(noteEvent.NoteBody)

	// If this is a command note AND we have a transactional command processor,
	// wrap hook_event insertion and command execution in one atomic transaction.
	if isCmd {
		if h.commandProcessor == nil {
			// Still insert the hook event for audit outside the command path.
			if _, insertErr := h.insertHookEvent(ctx, db.New(h.db), deliveryKey, hookSource, parsed, payload, "verified", ""); insertErr != nil {
				if !isDuplicateKeyError(insertErr) {
					l.WarnContext(ctx, "failed to insert hook_event for note",
						"delivery_key", deliveryKey,
						"error", insertErr,
					)
				}
			}
			h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", eventType)
			l.WarnContext(ctx, "note command received but no command processor configured",
				"delivery_key", deliveryKey,
				"note_body", noteEvent.NoteBody,
			)
			writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "command": "unprocessed"})
			return
		}

		cmd := parseNoteCommand(noteEvent.NoteBody)

		err = db.RunTx(ctx, h.db, func(ctx context.Context, q *db.Queries) error {
			if _, insertErr := h.insertHookEvent(ctx, q, deliveryKey, hookSource, parsed, payload, "verified", ""); insertErr != nil {
				return insertErr
			}
			return h.commandProcessor.ExecuteWithQuerier(ctx, q, noteEvent, cmd)
		})

		if err != nil {
			if isDuplicateKeyError(err) {
				l.InfoContext(ctx, "duplicate delivery key on note command insert, acknowledging",
					"delivery_key", deliveryKey,
				)
				h.writeAuditLog(ctx, deliveryKey, hookSource, "deduplicated", "", eventType)
				writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
				return
			}
			l.ErrorContext(ctx, "command execution failed",
				"delivery_key", deliveryKey,
				"note_body", noteEvent.NoteBody,
				"error", err,
			)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "command execution failed"})
			return
		}

		h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", eventType)
		l.InfoContext(ctx, "note command processed",
			"delivery_key", deliveryKey,
			"mr_iid", noteEvent.MRIID,
			"note_body", noteEvent.NoteBody,
		)
		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "command": "processed"})
		return
	}

	// Non-command note: insert hook_event for audit.
	if _, insertErr := h.insertHookEvent(ctx, db.New(h.db), deliveryKey, hookSource, parsed, payload, "verified", ""); insertErr != nil {
		if !isDuplicateKeyError(insertErr) {
			l.WarnContext(ctx, "failed to insert hook_event for note",
				"delivery_key", deliveryKey,
				"error", insertErr,
			)
		}
	}
	h.writeAuditLog(ctx, deliveryKey, hookSource, "verified", "", eventType)

	l.InfoContext(ctx, "note event accepted, no command found",
		"delivery_key", deliveryKey,
		"mr_iid", noteEvent.MRIID,
	)
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "command": "none"})
}

// isCommandNote returns true if the note body starts with /ai-review.
func isCommandNote(noteBody string) bool {
	return strings.HasPrefix(strings.TrimSpace(noteBody), "/ai-review")
}

// parseNoteCommand is a thin wrapper that extracts the command kind and args
// from a note body. Returns nil if the body is not a command.
func parseNoteCommand(noteBody string) interface{} {
	trimmed := strings.TrimSpace(noteBody)
	if !strings.HasPrefix(trimmed, "/ai-review") {
		return nil
	}

	// Extract the command portion after "/ai-review".
	rest := strings.TrimSpace(trimmed[len("/ai-review"):])

	parts := strings.SplitN(rest, " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		return &noteCommand{kind: "unknown"}
	}

	keyword := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	return &noteCommand{kind: keyword, args: args}
}

// noteCommand is an opaque command value passed through the CommandProcessor
// interface to avoid a direct dependency on the commands package from hooks.
type noteCommand struct {
	kind string
	args string
}

// Kind returns the command keyword.
func (c *noteCommand) Kind() string { return c.kind }

// Args returns the command arguments.
func (c *noteCommand) Args() string { return c.args }

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
// Deprecated: use IsMergeRequestEventType and IsSystemHookHeader instead for
// proper system hook handling. Kept for backward compatibility with existing tests.
func isMergeRequestEvent(eventType string) bool {
	return IsMergeRequestEventType(eventType)
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
