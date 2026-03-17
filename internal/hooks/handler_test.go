package hooks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
)

const migrationsDir = "../../migrations"

// testWebhookKey is a dummy webhook verification key used exclusively in tests.
// It is NOT a real secret.
const testWebhookKey = "CHANGEME" //nolint:gosec

// testLogger returns a silent logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// setupTestDB creates a fresh MySQL container with migrations applied.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

func newTestHandler(sqlDB *sql.DB) *Handler {
	return NewHandler(testLogger(), sqlDB, testWebhookKey, nil)
}

// postWebhook sends a POST request to the handler with the given headers and body.
func postWebhook(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func webhookHeaders(token string, extra map[string]string) map[string]string {
	headers := map[string]string{
		"X-Gitlab-Token": token,
		"X-Gitlab-Event": "Merge Request Hook",
		"Content-Type":   "application/json",
	}
	for k, v := range extra {
		headers[k] = v
	}
	return headers
}

// mrOpenPayload returns a minimal valid MR open webhook payload.
func mrOpenPayload(dlvID string) string {
	return `{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"user": {"username": "testuser"},
		"project": {
			"id": 42,
			"path_with_namespace": "test/repo",
			"web_url": "https://gitlab.example.com/test/repo"
		},
		"object_attributes": {
			"iid": 7,
			"action": "open",
			"state": "opened",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"url": "https://gitlab.example.com/test/repo/-/merge_requests/7",
			"last_commit": {"id": "abc123def456"}
		}
	}`
}

// pipelinePayload returns a minimal pipeline webhook payload (non-MR).
func pipelinePayload() string {
	return `{
		"object_kind": "pipeline",
		"object_attributes": {
			"id": 999,
			"status": "success"
		},
		"project": {"id": 42}
	}`
}

// TestWebhookAuth verifies VAL-INGRESS-001 and VAL-INGRESS-002:
// - A valid token returns 200 and creates a hook_events row.
// - A missing token returns 401 and does not create a hook_events row.
// - An incorrect token returns 401 and does not create a hook_events row.
func TestWebhookAuth(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	t.Run("valid token returns 200", func(t *testing.T) {
		dlvID := "dk-valid-200"
		rec := postWebhook(handler, mrOpenPayload(dlvID), map[string]string{
			"X-Gitlab-Token":    secret,
			"X-Gitlab-Event":    "Merge Request Hook",
			"X-Gitlab-Delivery": dlvID,
			"Content-Type":      "application/json",
		})

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		// Verify hook_events row was created.
		event, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), dlvID)
		if err != nil {
			t.Fatalf("expected hook_events row, got error: %v", err)
		}
		if event.VerificationOutcome != "verified" {
			t.Errorf("expected verification_outcome='verified', got %q", event.VerificationOutcome)
		}
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		dlvID := "dk-noheader-401"
		rec := postWebhook(handler, mrOpenPayload(dlvID), map[string]string{
			"X-Gitlab-Event":    "Merge Request Hook",
			"X-Gitlab-Delivery": dlvID,
			"Content-Type":      "application/json",
		})

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}

		// Verify no hook_events row was created.
		_, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), dlvID)
		if err == nil {
			t.Error("expected no hook_events row for missing token, but one was found")
		}
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		dlvID := "dk-badvalue-401"
		rec := postWebhook(handler, mrOpenPayload(dlvID), map[string]string{
			"X-Gitlab-Token":    "BADVALUE",
			"X-Gitlab-Event":    "Merge Request Hook",
			"X-Gitlab-Delivery": dlvID,
			"Content-Type":      "application/json",
		})

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}

		// Verify no hook_events row was created.
		_, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), dlvID)
		if err == nil {
			t.Error("expected no hook_events row for wrong token, but one was found")
		}
	})
}

// TestMalformedJSON verifies VAL-INGRESS-009:
// A POST with invalid JSON returns 400, no database records are created.
func TestMalformedJSON(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"invalid json", "{not json at all"},
		{"truncated json", `{"object_kind": "merge_reque`},
		{"plain text", "hello world"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dlvID := "malformed-" + tc.name
			rec := postWebhook(handler, tc.body, map[string]string{
				"X-Gitlab-Token":    secret,
				"X-Gitlab-Event":    "Merge Request Hook",
				"X-Gitlab-Delivery": dlvID,
				"Content-Type":      "application/json",
			})

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			// Verify no hook_events row.
			_, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), dlvID)
			if err == nil {
				t.Error("expected no hook_events row for malformed JSON, but one was found")
			}
		})
	}
}

// TestWebhookDeliveryKeyHeaders verifies modern and legacy GitLab delivery key
// header extraction, including preference order and synthetic fallback.
func TestWebhookDeliveryKeyHeaders(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name      string
		headers   map[string]string
		want      string
		synthetic bool
	}{
		{
			name: "prefers webhook UUID over other headers",
			headers: map[string]string{
				"X-Gitlab-Webhook-UUID": "webhook-uuid-1",
				"X-Gitlab-Delivery":     "delivery-uuid-1",
				"X-Gitlab-Event-UUID":   "event-uuid-1",
			},
			want: "webhook-uuid-1",
		},
		{
			name: "falls back to delivery header",
			headers: map[string]string{
				"X-Gitlab-Delivery": "delivery-uuid-2",
			},
			want: "delivery-uuid-2",
		},
		{
			name: "falls back to legacy event UUID header",
			headers: map[string]string{
				"X-Gitlab-Event-UUID": "event-uuid-2",
			},
			want: "event-uuid-2",
		},
		{
			name:      "generates synthetic key when no supported header is present",
			headers:   map[string]string{},
			synthetic: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got := h.extractDeliveryKey(req)
			if tc.synthetic {
				if !strings.HasPrefix(got, "synthetic-") {
					t.Fatalf("expected synthetic delivery key, got %q", got)
				}
				if _, err := uuid.Parse(strings.TrimPrefix(got, "synthetic-")); err != nil {
					t.Fatalf("synthetic delivery key should end with a UUID, got %q: %v", got, err)
				}
				return
			}

			if got != tc.want {
				t.Fatalf("extractDeliveryKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDuplicateDeliveryKey verifies VAL-INGRESS-006:
// Replaying the same delivery key returns 200 without creating duplicate records,
// including when equivalent deliveries arrive through different supported GitLab headers.
func TestDuplicateDeliveryKey(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	tests := []struct {
		name          string
		deliveryKey   string
		firstHeaders  map[string]string
		secondHeaders map[string]string
	}{
		{
			name:        "webhook UUID dedupes against delivery fallback",
			deliveryKey: "dedup-webhook-uuid-1",
			firstHeaders: webhookHeaders(secret, map[string]string{
				"X-Gitlab-Webhook-UUID": "dedup-webhook-uuid-1",
			}),
			secondHeaders: webhookHeaders(secret, map[string]string{
				"X-Gitlab-Delivery": "dedup-webhook-uuid-1",
			}),
		},
		{
			name:        "delivery header dedupes against legacy event UUID fallback",
			deliveryKey: "dedup-delivery-1",
			firstHeaders: webhookHeaders(secret, map[string]string{
				"X-Gitlab-Delivery": "dedup-delivery-1",
			}),
			secondHeaders: webhookHeaders(secret, map[string]string{
				"X-Gitlab-Event-UUID": "dedup-delivery-1",
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := mrOpenPayload(tc.deliveryKey)

			// First request should succeed and create records.
			rec1 := postWebhook(handler, payload, tc.firstHeaders)
			if rec1.Code != http.StatusOK {
				t.Fatalf("first request: expected 200, got %d: %s", rec1.Code, rec1.Body.String())
			}

			// Second equivalent request should be deduped even if it uses a different supported header.
			rec2 := postWebhook(handler, payload, tc.secondHeaders)
			if rec2.Code != http.StatusOK {
				t.Fatalf("second request: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["status"] != "duplicate" {
				t.Errorf("expected status='duplicate', got %q", resp["status"])
			}

			event, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), tc.deliveryKey)
			if err != nil {
				t.Fatalf("expected hook event for %q: %v", tc.deliveryKey, err)
			}
			if event.DeliveryKey != tc.deliveryKey {
				t.Fatalf("stored delivery key = %q, want %q", event.DeliveryKey, tc.deliveryKey)
			}

			var count int
			err = sqlDB.QueryRowContext(context.Background(),
				"SELECT COUNT(*) FROM hook_events WHERE delivery_key = ?", tc.deliveryKey,
			).Scan(&count)
			if err != nil {
				t.Fatalf("count query: %v", err)
			}
			if count != 1 {
				t.Errorf("expected 1 hook_events row, got %d", count)
			}
		})
	}
}

// TestIgnoreUnknownEvent verifies VAL-INGRESS-010:
// A non-MR event (pipeline, push) returns 200 but no review run is created.
func TestIgnoreUnknownEvent(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	tests := []struct {
		name      string
		eventType string
		body      string
	}{
		{
			name:      "pipeline event",
			eventType: "Pipeline Hook",
			body:      pipelinePayload(),
		},
		{
			name:      "push event",
			eventType: "Push Hook",
			body:      `{"object_kind":"push","ref":"refs/heads/main","project":{"id":42}}`,
		},
		{
			name:      "tag push event",
			eventType: "Tag Push Hook",
			body:      `{"object_kind":"tag_push","ref":"refs/tags/v1.0","project":{"id":42}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dlvID := "unknown-" + tc.name
			rec := postWebhook(handler, tc.body, map[string]string{
				"X-Gitlab-Token":    secret,
				"X-Gitlab-Event":    tc.eventType,
				"X-Gitlab-Delivery": dlvID,
				"Content-Type":      "application/json",
			})

			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}

			// Verify hook_event IS created for audit trail.
			_, err := db.New(sqlDB).GetHookEventByDeliveryKey(context.Background(), dlvID)
			if err != nil {
				t.Errorf("expected hook_events row for audit trail, got error: %v", err)
			}

			// Verify no review_runs were created.
			var runCount int
			err = sqlDB.QueryRowContext(context.Background(),
				"SELECT COUNT(*) FROM review_runs",
			).Scan(&runCount)
			if err != nil {
				t.Fatalf("count review_runs: %v", err)
			}
			if runCount != 0 {
				t.Errorf("expected 0 review_runs for non-MR event, got %d", runCount)
			}

			// Verify the response indicates ignored.
			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["status"] != "ignored" {
				t.Errorf("expected status='ignored', got %q", resp["status"])
			}
		})
	}
}

// TestWebhookAuditLogging verifies VAL-OBS-001:
// Every webhook receipt writes an audit record with delivery key, hook source,
// verification outcome, and rejection reason when applicable.
func TestWebhookAuditLogging(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	t.Run("accepted webhook has audit record", func(t *testing.T) {
		dlvID := "audit-accepted-webhook-uuid-1"
		postWebhook(handler, mrOpenPayload(dlvID), webhookHeaders(secret, map[string]string{
			"X-Gitlab-Webhook-UUID": dlvID,
		}))

		logs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(context.Background(), dlvID)
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}
		if len(logs) == 0 {
			t.Fatal("expected at least 1 audit log record")
		}

		found := false
		for _, log := range logs {
			if log.VerificationOutcome == "verified" && log.DeliveryKey == dlvID {
				found = true
				if log.HookSource == "" {
					t.Error("expected non-empty hook_source in audit log")
				}
				break
			}
		}
		if !found {
			t.Error("no audit log with verification_outcome='verified' found")
		}
	})

	t.Run("rejected webhook has audit record with reason", func(t *testing.T) {
		dlvID := "audit-rejected-1"
		postWebhook(handler, mrOpenPayload(dlvID), webhookHeaders("BADVALUE", map[string]string{
			"X-Gitlab-Delivery": dlvID,
		}))

		logs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(context.Background(), dlvID)
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}
		if len(logs) == 0 {
			t.Fatal("expected at least 1 audit log record for rejection")
		}

		found := false
		for _, log := range logs {
			if log.VerificationOutcome == "rejected" && log.RejectionReason != "" {
				found = true
				if log.DeliveryKey != dlvID {
					t.Errorf("expected delivery_key=%q, got %q", dlvID, log.DeliveryKey)
				}
				break
			}
		}
		if !found {
			t.Error("no audit log with verification_outcome='rejected' and rejection_reason found")
		}
	})

	t.Run("malformed JSON has audit record", func(t *testing.T) {
		dlvID := "audit-malformed-event-uuid-1"
		postWebhook(handler, "{bad json", webhookHeaders(secret, map[string]string{
			"X-Gitlab-Event-UUID": dlvID,
		}))

		logs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(context.Background(), dlvID)
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}
		if len(logs) == 0 {
			t.Fatal("expected at least 1 audit log record for malformed JSON")
		}

		found := false
		for _, log := range logs {
			if log.VerificationOutcome == "rejected" && log.RejectionReason == "malformed_json" {
				found = true
				break
			}
		}
		if !found {
			t.Error("no audit log with rejection_reason='malformed_json' found")
		}
	})

	t.Run("duplicate delivery has audit record", func(t *testing.T) {
		dlvID := "audit-dedup-cross-variant-1"
		payload := mrOpenPayload(dlvID)

		// First: accepted
		postWebhook(handler, payload, webhookHeaders(secret, map[string]string{
			"X-Gitlab-Webhook-UUID": dlvID,
		}))
		// Second: deduplicated
		postWebhook(handler, payload, webhookHeaders(secret, map[string]string{
			"X-Gitlab-Delivery": dlvID,
		}))

		logs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(context.Background(), dlvID)
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}

		var hasVerified, hasDedup bool
		for _, log := range logs {
			switch log.VerificationOutcome {
			case "verified":
				hasVerified = true
			case "deduplicated":
				hasDedup = true
			}
		}
		if !hasVerified {
			t.Error("expected audit log with outcome='verified'")
		}
		if !hasDedup {
			t.Error("expected audit log with outcome='deduplicated'")
		}
	})
}

// TestPayloadSizeLimit verifies that excessively large payloads are rejected.
func TestPayloadSizeLimit(t *testing.T) {
	sqlDB := setupTestDB(t)
	secret := testWebhookKey
	handler := newTestHandler(sqlDB)

	// Create a payload just over the limit.
	bigPayload := `{"object_kind":"merge_request","data":"` + strings.Repeat("x", maxPayloadBytes) + `"}`

	rec := postWebhook(handler, bigPayload, map[string]string{
		"X-Gitlab-Token":    secret,
		"X-Gitlab-Event":    "Merge Request Hook",
		"X-Gitlab-Delivery": "big-payload-1",
		"Content-Type":      "application/json",
	})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized payload, got %d", rec.Code)
	}
}

// TestParseWebhookPayload verifies field extraction from various payload shapes.
func TestParseWebhookPayload(t *testing.T) {
	t.Run("standard MR payload", func(t *testing.T) {
		payload := json.RawMessage(`{
			"object_kind": "merge_request",
			"event_type": "merge_request",
			"project": {"id": 42},
			"object_attributes": {
				"iid": 7,
				"action": "open",
				"last_commit": {"id": "abc123"}
			}
		}`)

		parsed := parseWebhookPayload(payload, "Merge Request Hook", "project")
		if parsed.EventType != "Merge Request Hook" {
			t.Errorf("EventType = %q, want 'Merge Request Hook'", parsed.EventType)
		}
		if parsed.Action != "open" {
			t.Errorf("Action = %q, want 'open'", parsed.Action)
		}
		if parsed.ProjectID != 42 {
			t.Errorf("ProjectID = %d, want 42", parsed.ProjectID)
		}
		if parsed.MRIID != 7 {
			t.Errorf("MRIID = %d, want 7", parsed.MRIID)
		}
		if parsed.HeadSHA != "abc123" {
			t.Errorf("HeadSHA = %q, want 'abc123'", parsed.HeadSHA)
		}
	})

	t.Run("missing last_commit", func(t *testing.T) {
		payload := json.RawMessage(`{
			"object_kind": "merge_request",
			"project": {"id": 42},
			"object_attributes": {
				"iid": 7,
				"action": "open"
			}
		}`)

		parsed := parseWebhookPayload(payload, "Merge Request Hook", "project")
		if parsed.HeadSHA != "" {
			t.Errorf("HeadSHA should be empty when last_commit is missing, got %q", parsed.HeadSHA)
		}
	})

	t.Run("fallback to object_kind when header empty", func(t *testing.T) {
		payload := json.RawMessage(`{
			"object_kind": "merge_request",
			"project": {"id": 42},
			"object_attributes": {"iid": 7, "action": "open"}
		}`)

		parsed := parseWebhookPayload(payload, "", "project")
		if parsed.EventType != "merge_request" {
			t.Errorf("EventType = %q, want 'merge_request'", parsed.EventType)
		}
	})
}

// TestIsMergeRequestEvent verifies event type classification.
func TestIsMergeRequestEvent(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"merge_request", true},
		{"Merge Request Hook", true},
		{"merge request hook", true},
		{"Pipeline Hook", false},
		{"Push Hook", false},
		{"Tag Push Hook", false},
		{"Note Hook", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := isMergeRequestEvent(tc.input)
			if got != tc.want {
				t.Errorf("isMergeRequestEvent(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestDetectHookSource verifies hook source detection from headers.
func TestDetectHookSource(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  string
	}{
		{"system hook", "System Hook", "system"},
		{"project hook", "Merge Request Hook", "project"},
		{"empty header", "", "project"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
			if tc.event != "" {
				req.Header.Set("X-Gitlab-Event", tc.event)
			}
			got := detectHookSource(req)
			if got != tc.want {
				t.Errorf("detectHookSource = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVerifyTokenConstantTime verifies token comparison behavior.
func TestVerifyTokenConstantTime(t *testing.T) {
	h := &Handler{secret: testWebhookKey}

	if !h.verifyToken(testWebhookKey) {
		t.Error("expected valid token to pass")
	}
	if h.verifyToken("BADVALUE") {
		t.Error("expected wrong token to fail")
	}
	if h.verifyToken("") {
		t.Error("expected empty token to fail")
	}

	h2 := &Handler{secret: ""}
	if h2.verifyToken("BADVALUE") {
		t.Error("expected empty secret to reject all tokens")
	}
}
