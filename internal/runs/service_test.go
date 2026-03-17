package runs

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
)

const migrationsDir = "../../migrations"

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

// makeOpenEvent returns a NormalizedEvent for an MR open action.
func makeOpenEvent(headSHA string) hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		makePayload("open", headSHA, false),
		"Merge Request Hook", "project",
	)
	return ev
}

// makeUpdateEvent returns a NormalizedEvent for an MR update action with
// a new head SHA (simulating a code push with oldrev).
func makeUpdateEvent(headSHA string) hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		makePayload("update", headSHA, false),
		"Merge Request Hook", "project",
	)
	return ev
}

// makeCloseEvent returns a NormalizedEvent for an MR close action.
func makeCloseEvent() hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		makePayload("close", "abc123def456", false),
		"Merge Request Hook", "project",
	)
	return ev
}

// makeMergeEvent returns a NormalizedEvent for an MR merge action.
func makeMergeEvent() hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		makeMergePayload(),
		"Merge Request Hook", "project",
	)
	return ev
}

// makePayload builds a minimal MR webhook payload.
func makePayload(action, headSHA string, isDraft bool) []byte {
	draft := "false"
	if isDraft {
		draft = "true"
	}
	lastCommit := ""
	if headSHA != "" {
		lastCommit = `,"last_commit":{"id":"` + headSHA + `"}`
	}
	return []byte(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"object_attributes": {
			"iid": 42,
			"action": "` + action + `",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "opened",
			"draft": ` + draft + `,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42"` + lastCommit + `
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// makeMergePayload builds a payload for a merge event (state="merged").
func makeMergePayload() []byte {
	return []byte(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"object_attributes": {
			"iid": 42,
			"action": "merge",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "merged",
			"draft": false,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42",
			"last_commit": {"id": "abc123def456"}
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// TestOpenCreatesPendingRun verifies VAL-INGRESS-003:
// An MR open event creates a review_runs row with status 'pending'.
func TestOpenCreatesPendingRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	ev := makeOpenEvent("abc123def456")

	if err := svc.ProcessEvent(context.Background(), ev, 0); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	// Verify review_runs row exists with status=pending.
	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}

	if run.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", run.Status)
	}
	if run.HeadSha != "abc123def456" {
		t.Errorf("expected head_sha 'abc123def456', got %q", run.HeadSha)
	}
	if run.TriggerType != "webhook" {
		t.Errorf("expected trigger_type 'webhook', got %q", run.TriggerType)
	}

	// Verify merge_request row was created.
	mr, err := db.New(sqlDB).GetMergeRequest(context.Background(), run.MergeRequestID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if mr.MrIid != 42 {
		t.Errorf("expected mr_iid=42, got %d", mr.MrIid)
	}
	if mr.HeadSha != "abc123def456" {
		t.Errorf("expected mr head_sha='abc123def456', got %q", mr.HeadSha)
	}
}

// TestUpdateCreatesNewHeadRun verifies VAL-INGRESS-004:
// An MR update with a new commit (oldrev present) creates a new review run
// for the new HEAD SHA.
func TestUpdateCreatesNewHeadRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	// First: open the MR with an initial SHA.
	openEv := makeOpenEvent("sha_initial")
	if err := svc.ProcessEvent(context.Background(), openEv, 0); err != nil {
		t.Fatalf("ProcessEvent open: %v", err)
	}

	// Second: update with a new HEAD SHA (simulating code push with oldrev).
	updateEv := makeUpdateEvent("sha_new_commit")
	if err := svc.ProcessEvent(context.Background(), updateEv, 0); err != nil {
		t.Fatalf("ProcessEvent update: %v", err)
	}

	// Verify two review runs exist.
	openRun, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), openEv.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey (open): %v", err)
	}
	updateRun, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), updateEv.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey (update): %v", err)
	}

	if openRun.ID == updateRun.ID {
		t.Fatal("expected different run IDs for open and update events")
	}

	if openRun.HeadSha != "sha_initial" {
		t.Errorf("open run: expected head_sha='sha_initial', got %q", openRun.HeadSha)
	}
	if updateRun.HeadSha != "sha_new_commit" {
		t.Errorf("update run: expected head_sha='sha_new_commit', got %q", updateRun.HeadSha)
	}
	if updateRun.Status != "pending" {
		t.Errorf("update run: expected status='pending', got %q", updateRun.Status)
	}
}

// TestCloseCancelsRuns verifies VAL-INGRESS-005 (close case):
// A close event cancels any pending or running review runs for that MR.
func TestCloseCancelsRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	// Create a pending run via open event.
	openEv := makeOpenEvent("abc123def456")
	if err := svc.ProcessEvent(context.Background(), openEv, 0); err != nil {
		t.Fatalf("ProcessEvent open: %v", err)
	}

	// Verify it's pending.
	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), openEv.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}
	if run.Status != "pending" {
		t.Fatalf("expected pending, got %q", run.Status)
	}

	// Close the MR.
	closeEv := makeCloseEvent()
	if err := svc.ProcessEvent(context.Background(), closeEv, 0); err != nil {
		t.Fatalf("ProcessEvent close: %v", err)
	}

	// Verify run is now cancelled.
	run, err = db.New(sqlDB).GetReviewRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", run.Status)
	}

	// Verify MR state updated to closed.
	mr, err := db.New(sqlDB).GetMergeRequest(context.Background(), run.MergeRequestID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if mr.State != "closed" {
		t.Errorf("expected MR state 'closed', got %q", mr.State)
	}
}

// TestMergeCancelsRuns verifies VAL-INGRESS-005 (merge case):
// A merge event cancels any pending or running review runs for that MR.
func TestMergeCancelsRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	// Create a pending run via open event.
	openEv := makeOpenEvent("abc123def456")
	if err := svc.ProcessEvent(context.Background(), openEv, 0); err != nil {
		t.Fatalf("ProcessEvent open: %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), openEv.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}

	// Merge the MR.
	mergeEv := makeMergeEvent()
	if err := svc.ProcessEvent(context.Background(), mergeEv, 0); err != nil {
		t.Fatalf("ProcessEvent merge: %v", err)
	}

	// Verify run is now cancelled.
	run, err = db.New(sqlDB).GetReviewRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", run.Status)
	}

	// Verify MR state updated to merged.
	mr, err := db.New(sqlDB).GetMergeRequest(context.Background(), run.MergeRequestID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if mr.State != "merged" {
		t.Errorf("expected MR state 'merged', got %q", mr.State)
	}
}

// TestReplayDoesNotDuplicateRun verifies VAL-INGRESS-006:
// A replayed webhook with the same idempotency key does not create a second
// review run.
func TestReplayDoesNotDuplicateRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	ev := makeOpenEvent("abc123def456")

	// First call: should create a run.
	if err := svc.ProcessEvent(context.Background(), ev, 0); err != nil {
		t.Fatalf("ProcessEvent (first): %v", err)
	}

	// Second call: same idempotency key, should NOT create a second run.
	if err := svc.ProcessEvent(context.Background(), ev, 0); err != nil {
		t.Fatalf("ProcessEvent (second): %v", err)
	}

	// Verify only one review_run exists for this idempotency key.
	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}

	// Count all runs for the MR.
	runs, err := db.New(sqlDB).ListReviewRunsByMR(context.Background(), run.MergeRequestID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected exactly 1 review run, got %d", len(runs))
	}
}

// TestDraftMRCreatesRun verifies VAL-INGRESS-011:
// A draft MR event creates a review run normally.
func TestDraftMRCreatesRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	ev, _ := hooks.NormalizeWebhook(
		makePayload("open", "abc123def456", true),
		"Merge Request Hook", "project",
	)

	if err := svc.ProcessEvent(context.Background(), ev, 0); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}
	if run.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", run.Status)
	}

	// Verify draft is preserved on the MR.
	mr, err := db.New(sqlDB).GetMergeRequest(context.Background(), run.MergeRequestID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if !mr.IsDraft {
		t.Error("expected MR is_draft=true")
	}
}

// TestCancelNoOpWhenNoRuns verifies that cancellation is a no-op when no
// project or MR exists yet.
func TestCancelNoOpWhenNoRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	closeEv := makeCloseEvent()
	if err := svc.ProcessEvent(context.Background(), closeEv, 0); err != nil {
		t.Fatalf("ProcessEvent close on nonexistent MR: %v", err)
	}
	// No error means success — cancel is a no-op.
}

// TestHookEventIDLinked verifies that when a hookEventID is provided, it is
// stored on the review_run.
func TestHookEventIDLinked(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	ev := makeOpenEvent("abc123def456")

	// Insert a fake hook_event to get a valid ID.
	result, err := db.New(sqlDB).InsertHookEvent(context.Background(), db.InsertHookEventParams{
		DeliveryKey:         "dk-lifecycle-test-1",
		HookSource:          "project",
		EventType:           "merge_request",
		ProjectID:           sql.NullInt64{Int64: 100, Valid: true},
		MrIid:               sql.NullInt64{Int64: 42, Valid: true},
		Action:              "open",
		HeadSha:             "abc123def456",
		Payload:             makePayload("open", "abc123def456", false),
		VerificationOutcome: "verified",
	})
	if err != nil {
		t.Fatalf("InsertHookEvent: %v", err)
	}
	hookEvID, _ := result.LastInsertId()

	if err := svc.ProcessEvent(context.Background(), ev, hookEvID); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}
	if !run.HookEventID.Valid || run.HookEventID.Int64 != hookEvID {
		t.Errorf("expected hook_event_id=%d, got %v", hookEvID, run.HookEventID)
	}
}

// TestMultipleRunsDifferentSHAs verifies that open + update with different
// SHAs creates separate runs, and close cancels all of them.
func TestMultipleRunsDifferentSHAs(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	// Open with SHA1.
	ev1 := makeOpenEvent("sha1")
	if err := svc.ProcessEvent(context.Background(), ev1, 0); err != nil {
		t.Fatalf("ProcessEvent open: %v", err)
	}

	// Update with SHA2.
	ev2 := makeUpdateEvent("sha2")
	if err := svc.ProcessEvent(context.Background(), ev2, 0); err != nil {
		t.Fatalf("ProcessEvent update: %v", err)
	}

	// Verify two runs exist.
	run1, _ := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev1.IdempotencyKey)
	run2, _ := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev2.IdempotencyKey)

	if run1.ID == run2.ID {
		t.Fatal("expected two different runs")
	}

	// Close: should cancel both.
	closeEv := makeCloseEvent()
	if err := svc.ProcessEvent(context.Background(), closeEv, 0); err != nil {
		t.Fatalf("ProcessEvent close: %v", err)
	}

	run1, _ = db.New(sqlDB).GetReviewRun(context.Background(), run1.ID)
	run2, _ = db.New(sqlDB).GetReviewRun(context.Background(), run2.ID)

	if run1.Status != "cancelled" {
		t.Errorf("run1: expected 'cancelled', got %q", run1.Status)
	}
	if run2.Status != "cancelled" {
		t.Errorf("run2: expected 'cancelled', got %q", run2.Status)
	}
}
