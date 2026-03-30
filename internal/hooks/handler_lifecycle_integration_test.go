package hooks_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/runs"
)

const lifecycleMigrationsDir = "../../migrations"

func lifecycleTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupLifecycleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, lifecycleMigrationsDir)
	return sqlDB
}

func newLifecycleHandler(sqlDB *sql.DB) *hooks.Handler {
	logger := lifecycleTestLogger()
	return hooks.NewHandler(logger, sqlDB, lifecycleTestWebhookKey, runs.NewService(logger, sqlDB))
}

func postLifecycleWebhook(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func lifecycleHeaders(deliveryKey, eventType string) map[string]string {
	return map[string]string{
		"X-Gitlab-Token":    lifecycleTestWebhookKey,
		"X-Gitlab-Event":    eventType,
		"X-Gitlab-Delivery": deliveryKey,
		"Content-Type":      "application/json",
	}
}

const lifecycleTestWebhookKey = "test-webhook-sample" //nolint:gosec

func lifecycleOpenPayload() string {
	return `{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"user": {"username": "testuser"},
		"project": {
			"id": 42,
			"path_with_namespace": "sample/repo",
			"web_url": "https://gitlab.example.com/sample/repo"
		},
		"object_attributes": {
			"iid": 7,
			"action": "open",
			"state": "opened",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"url": "https://gitlab.example.com/sample/repo/-/merge_requests/7",
			"last_commit": {"id": "head-sha-sample-001"}
		}
	}`
}

func lifecycleUpdatePayload(newHeadSHA string) string {
	return fmt.Sprintf(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"user": {"username": "testuser"},
		"project": {
			"id": 42,
			"path_with_namespace": "sample/repo",
			"web_url": "https://gitlab.example.com/sample/repo"
		},
		"object_attributes": {
			"iid": 7,
			"action": "update",
			"state": "opened",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"url": "https://gitlab.example.com/sample/repo/-/merge_requests/7",
			"oldrev": "old-head-sha",
			"last_commit": {"id": %q}
		}
	}`, newHeadSHA)
}

func lifecyclePayload(action, state, headSHA string) string {
	lastCommit := ""
	if headSHA != "" {
		lastCommit = fmt.Sprintf(`,
			"last_commit": {"id": %q}`, headSHA)
	}

	return fmt.Sprintf(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"user": {"username": "testuser"},
		"project": {
			"id": 42,
			"path_with_namespace": "sample/repo",
			"web_url": "https://gitlab.example.com/sample/repo"
		},
		"object_attributes": {
			"iid": 7,
			"action": %q,
			"state": %q,
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"url": "https://gitlab.example.com/sample/repo/-/merge_requests/7"%s
		}
	}`, action, state, lastCommit)
}

func lifecycleScopedPayload(action, state, headSHA, updatedAt, oldrev string, groupScoped bool) string {
	lastCommit := ""
	if headSHA != "" {
		lastCommit = fmt.Sprintf(`,
			"last_commit": {"id": %q}`, headSHA)
	}

	updatedAtField := ""
	if updatedAt != "" {
		updatedAtField = fmt.Sprintf(`,
			"updated_at": %q,
			"created_at": "2026-03-17T10:00:00Z"`, updatedAt)
	}

	oldrevField := ""
	if oldrev != "" {
		oldrevField = fmt.Sprintf(`,
			"oldrev": %q`, oldrev)
	}

	groupFields := ""
	if groupScoped {
		groupFields = `,
		"group_id": 24,
		"group_path": "test-group",
		"group_name": "Test Group"`
	}

	return fmt.Sprintf(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"user": {"username": "testuser"},
		"project": {
			"id": 42,
			"path_with_namespace": "sample/repo",
			"web_url": "https://gitlab.example.com/sample/repo"
		}%s,
		"object_attributes": {
			"iid": 7,
			"action": %q,
			"state": %q,
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"url": "https://gitlab.example.com/sample/repo/-/merge_requests/7"%s%s%s
		}
	}`,
		groupFields,
		action,
		state,
		updatedAtField,
		oldrevField,
		lastCommit,
	)
}

func lifecycleProjectAndMergeRequest(t *testing.T, sqlDB *sql.DB) (db.Project, db.MergeRequest) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	instance, err := queries.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}

	project, err := queries.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  42,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}

	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     7,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}

	return project, mr
}

func TestOpenCreatesPendingRun(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()
	deliveryKey := "dispatch-open-001"

	rec := postLifecycleWebhook(handler, lifecycleOpenPayload(), lifecycleHeaders(deliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	hookEvent, err := db.New(sqlDB).GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	reviewRuns, err := db.New(sqlDB).ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(reviewRuns) != 1 {
		t.Fatalf("expected 1 review run, got %d", len(reviewRuns))
	}

	run := reviewRuns[0]
	if run.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", run.Status)
	}
	if run.HeadSha != "head-sha-sample-001" {
		t.Errorf("expected head_sha 'head-sha-sample-001', got %q", run.HeadSha)
	}
	if !run.HookEventID.Valid || run.HookEventID.Int64 != hookEvent.ID {
		t.Errorf("expected hook_event_id=%d, got %v", hookEvent.ID, run.HookEventID)
	}
}

func TestUpdateCreatesNewHeadRun(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()

	openDeliveryKey := "lifecycle-update-open-1"
	rec := postLifecycleWebhook(handler, lifecycleOpenPayload(), lifecycleHeaders(openDeliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("open request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	updateDeliveryKey := "lifecycle-update-1"
	rec = postLifecycleWebhook(handler, lifecycleUpdatePayload("fedcba654321"), lifecycleHeaders(updateDeliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("update request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	reviewRuns, err := db.New(sqlDB).ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(reviewRuns) != 2 {
		t.Fatalf("expected 2 review runs, got %d", len(reviewRuns))
	}

	runsByHeadSHA := make(map[string]db.ReviewRun, len(reviewRuns))
	for _, run := range reviewRuns {
		runsByHeadSHA[run.HeadSha] = run
	}

	if run, ok := runsByHeadSHA["head-sha-sample-001"]; !ok {
		t.Fatal("expected open-event run for original HEAD SHA")
	} else {
		if run.Status != "cancelled" {
			t.Errorf("open-event run: expected status 'cancelled', got %q", run.Status)
		}
		if run.ErrorCode != "superseded_by_new_head" {
			t.Errorf("open-event run: expected error_code 'superseded_by_new_head', got %q", run.ErrorCode)
		}
	}

	if run, ok := runsByHeadSHA["fedcba654321"]; !ok {
		t.Fatal("expected update-event run for new HEAD SHA")
	} else if run.Status != "pending" {
		t.Errorf("update-event run: expected status 'pending', got %q", run.Status)
	}

	if mr.HeadSha != "fedcba654321" {
		t.Errorf("expected merge_request head_sha to update to 'fedcba654321', got %q", mr.HeadSha)
	}
}

func TestRuntimeProjectHookSource(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()
	deliveryKey := "runtime-project-source-1"

	rec := postLifecycleWebhook(handler, lifecycleOpenPayload(), lifecycleHeaders(deliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	hookEvent, err := db.New(sqlDB).GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}
	if hookEvent.HookSource != "project" {
		t.Fatalf("expected hook_event hook_source=project, got %q", hookEvent.HookSource)
	}

	auditLogs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("ListAuditLogsByDeliveryKey: %v", err)
	}
	if len(auditLogs) == 0 {
		t.Fatal("expected at least one audit log row")
	}
	if auditLogs[0].HookSource != "project" {
		t.Fatalf("expected audit hook_source=project, got %q", auditLogs[0].HookSource)
	}
}

func TestRuntimeGroupHookSource(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()
	deliveryKey := "runtime-group-source-1"
	payload := lifecycleScopedPayload("open", "opened", "head-sha-sample-001", "", "", true)

	rec := postLifecycleWebhook(handler, payload, lifecycleHeaders(deliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	hookEvent, err := db.New(sqlDB).GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}
	if hookEvent.HookSource != "group" {
		t.Fatalf("expected hook_event hook_source=group, got %q", hookEvent.HookSource)
	}

	auditLogs, err := db.New(sqlDB).ListAuditLogsByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("ListAuditLogsByDeliveryKey: %v", err)
	}
	if len(auditLogs) == 0 {
		t.Fatal("expected at least one audit log row")
	}
	if auditLogs[0].HookSource != "group" {
		t.Fatalf("expected audit hook_source=group, got %q", auditLogs[0].HookSource)
	}
}

func TestDeferredHeadSHAPersistence(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()
	deliveryKey := "runtime-deferred-head-persistence-1"
	payload := lifecycleScopedPayload("update", "opened", "", "2026-03-17T10:01:00Z", "old-head-sha", false)

	rec := postLifecycleWebhook(handler, payload, lifecycleHeaders(deliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	hookEvent, err := db.New(sqlDB).GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}
	if hookEvent.HeadSha != "" {
		t.Fatalf("expected deferred hook_event head_sha to be empty, got %q", hookEvent.HeadSha)
	}
	if !strings.Contains(string(hookEvent.Payload), `"oldrev": "old-head-sha"`) {
		t.Fatalf("expected stored payload to preserve deferred lookup fields, got %s", string(hookEvent.Payload))
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	reviewRuns, err := db.New(sqlDB).ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(reviewRuns) != 1 {
		t.Fatalf("expected 1 review run, got %d", len(reviewRuns))
	}

	run := reviewRuns[0]
	if run.HeadSha != "" {
		t.Fatalf("expected deferred review_run head_sha to be empty, got %q", run.HeadSha)
	}
	if !run.HookEventID.Valid || run.HookEventID.Int64 != hookEvent.ID {
		t.Fatalf("expected review_run to retain hook_event_id=%d, got %v", hookEvent.ID, run.HookEventID)
	}
	if run.IdempotencyKey == "" {
		t.Fatal("expected deferred review_run idempotency key to be populated")
	}
}

func TestDeferredHeadSHAIdempotency(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()

	projectOpen := lifecycleScopedPayload("open", "opened", "", "2026-03-17T10:00:00Z", "", false)
	groupOpen := lifecycleScopedPayload("open", "opened", "", "2026-03-17T10:00:00Z", "", true)
	updateMissingHead := lifecycleScopedPayload("update", "opened", "", "2026-03-17T10:05:00Z", "previous-head-sha", false)

	for _, tc := range []struct {
		deliveryKey string
		body        string
	}{
		{deliveryKey: "runtime-deferred-project-open-1", body: projectOpen},
		{deliveryKey: "runtime-deferred-group-open-1", body: groupOpen},
		{deliveryKey: "runtime-deferred-update-1", body: updateMissingHead},
	} {
		rec := postLifecycleWebhook(handler, tc.body, lifecycleHeaders(tc.deliveryKey, "Merge Request Hook"))
		if rec.Code != http.StatusOK {
			t.Fatalf("delivery %s: expected 200, got %d: %s", tc.deliveryKey, rec.Code, rec.Body.String())
		}
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	reviewRuns, err := db.New(sqlDB).ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(reviewRuns) != 2 {
		t.Fatalf("expected 2 review runs (one logical open trigger, one later deferred update), got %d", len(reviewRuns))
	}

	keys := map[string]struct{}{}
	for _, run := range reviewRuns {
		if run.HeadSha != "" {
			t.Fatalf("expected deferred review_run head_sha to stay empty, got %q", run.HeadSha)
		}
		keys[run.IdempotencyKey] = struct{}{}
	}
	if len(keys) != 2 {
		t.Fatalf("expected distinct idempotency keys for distinct deferred triggers, got %d unique keys", len(keys))
	}

	var hookEventCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM hook_events WHERE project_id = ? AND mr_iid = ?", 42, 7).Scan(&hookEventCount); err != nil {
		t.Fatalf("count hook_events: %v", err)
	}
	if hookEventCount != 3 {
		t.Fatalf("expected 3 persisted hook_events for the posted deferred deliveries, got %d", hookEventCount)
	}
}

func TestCloseCancelsRuns(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()

	openDeliveryKey := "lifecycle-close-open-1"
	updateDeliveryKey := "lifecycle-close-update-1"
	closeDeliveryKey := "lifecycle-close-1"

	if rec := postLifecycleWebhook(handler, lifecycleOpenPayload(), lifecycleHeaders(openDeliveryKey, "Merge Request Hook")); rec.Code != http.StatusOK {
		t.Fatalf("open request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postLifecycleWebhook(handler, lifecycleUpdatePayload("close-head-sha"), lifecycleHeaders(updateDeliveryKey, "Merge Request Hook")); rec.Code != http.StatusOK {
		t.Fatalf("update request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	queries := db.New(sqlDB)
	reviewRuns, err := queries.ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR before close: %v", err)
	}
	if len(reviewRuns) != 2 {
		t.Fatalf("expected 2 review runs before close, got %d", len(reviewRuns))
	}

	if err := queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{
		ID:          reviewRuns[0].ID,
		Status:      "running",
		ErrorCode:   "",
		ErrorDetail: sql.NullString{},
	}); err != nil {
		t.Fatalf("UpdateReviewRunStatus: %v", err)
	}

	rec := postLifecycleWebhook(handler, lifecyclePayload("close", "closed", "close-head-sha"), lifecycleHeaders(closeDeliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("close request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	reviewRuns, err = queries.ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR after close: %v", err)
	}
	for _, run := range reviewRuns {
		if run.Status != "cancelled" {
			t.Errorf("run %d: expected status 'cancelled', got %q", run.ID, run.Status)
		}
	}

	mr, err = queries.GetMergeRequest(ctx, mr.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if mr.State != "closed" {
		t.Errorf("expected merge_request state 'closed', got %q", mr.State)
	}
}

func TestMergeCancelsRuns(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()

	openDeliveryKey := "lifecycle-merge-open-1"
	updateDeliveryKey := "lifecycle-merge-update-1"
	mergeDeliveryKey := "lifecycle-merge-1"

	if rec := postLifecycleWebhook(handler, lifecycleOpenPayload(), lifecycleHeaders(openDeliveryKey, "Merge Request Hook")); rec.Code != http.StatusOK {
		t.Fatalf("open request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postLifecycleWebhook(handler, lifecycleUpdatePayload("merge-head-sha"), lifecycleHeaders(updateDeliveryKey, "Merge Request Hook")); rec.Code != http.StatusOK {
		t.Fatalf("update request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	queries := db.New(sqlDB)
	reviewRuns, err := queries.ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR before merge: %v", err)
	}
	if len(reviewRuns) != 2 {
		t.Fatalf("expected 2 review runs before merge, got %d", len(reviewRuns))
	}

	if err := queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{
		ID:          reviewRuns[0].ID,
		Status:      "running",
		ErrorCode:   "",
		ErrorDetail: sql.NullString{},
	}); err != nil {
		t.Fatalf("UpdateReviewRunStatus: %v", err)
	}

	rec := postLifecycleWebhook(handler, lifecyclePayload("merge", "merged", "merge-head-sha"), lifecycleHeaders(mergeDeliveryKey, "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("merge request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	reviewRuns, err = queries.ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR after merge: %v", err)
	}
	for _, run := range reviewRuns {
		if run.Status != "cancelled" {
			t.Errorf("run %d: expected status 'cancelled', got %q", run.ID, run.Status)
		}
	}

	mr, err = queries.GetMergeRequest(ctx, mr.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if mr.State != "merged" {
		t.Errorf("expected merge_request state 'merged', got %q", mr.State)
	}
}

func TestReplayDoesNotDuplicateRun(t *testing.T) {
	sqlDB := setupLifecycleTestDB(t)
	handler := newLifecycleHandler(sqlDB)
	ctx := context.Background()
	deliveryKey := "lifecycle-replay-1"
	headers := lifecycleHeaders(deliveryKey, "Merge Request Hook")
	payload := lifecycleOpenPayload()

	if rec := postLifecycleWebhook(handler, payload, headers); rec.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := postLifecycleWebhook(handler, payload, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("second request: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if response["status"] != "duplicate" {
		t.Fatalf("expected duplicate response status, got %q", response["status"])
	}

	_, mr := lifecycleProjectAndMergeRequest(t, sqlDB)
	reviewRuns, err := db.New(sqlDB).ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(reviewRuns) != 1 {
		t.Fatalf("expected 1 review run, got %d", len(reviewRuns))
	}

	var hookEventCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM hook_events WHERE delivery_key = ?", deliveryKey).Scan(&hookEventCount); err != nil {
		t.Fatalf("count hook_events: %v", err)
	}
	if hookEventCount != 1 {
		t.Errorf("expected 1 hook_event row, got %d", hookEventCount)
	}
}
