package reviewrun

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/runs"
)

const migrationsDir = "../../migrations"

func parityLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupParityDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

func makeOpenEvent(headSHA string) hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		[]byte(`{
			"object_kind": "merge_request",
			"event_type": "merge_request",
			"object_attributes": {
				"iid": 42,
				"action": "open",
				"title": "Add feature X",
				"source_branch": "feature-x",
				"target_branch": "main",
				"state": "opened",
				"draft": false,
				"url": "https://gitlab.example.com/samplegroup/samplerepo/-/merge_requests/42",
				"last_commit": {"id": "`+headSHA+`"}
			},
			"project": {
				"id": 100,
				"path_with_namespace": "samplegroup/samplerepo",
				"web_url": "https://gitlab.example.com/samplegroup/samplerepo"
			},
			"user": {"username": "johndoe"}
		}`),
		"Merge Request Hook", "project",
	)
	return ev
}

func makeUpdateEvent(headSHA string) hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		[]byte(`{
			"object_kind": "merge_request",
			"event_type": "merge_request",
			"object_attributes": {
				"iid": 42,
				"action": "update",
				"title": "Add feature X",
				"source_branch": "feature-x",
				"target_branch": "main",
				"state": "opened",
				"draft": false,
				"url": "https://gitlab.example.com/samplegroup/samplerepo/-/merge_requests/42",
				"last_commit": {"id": "`+headSHA+`"}
			},
			"project": {
				"id": 100,
				"path_with_namespace": "samplegroup/samplerepo",
				"web_url": "https://gitlab.example.com/samplegroup/samplerepo"
			},
			"user": {"username": "johndoe"}
		}`),
		"Merge Request Hook", "project",
	)
	return ev
}

func makeCloseEvent() hooks.NormalizedEvent {
	ev, _ := hooks.NormalizeWebhook(
		[]byte(`{
			"object_kind": "merge_request",
			"event_type": "merge_request",
			"object_attributes": {
				"iid": 42,
				"action": "close",
				"title": "Add feature X",
				"source_branch": "feature-x",
				"target_branch": "main",
				"state": "closed",
				"draft": false,
				"url": "https://gitlab.example.com/samplegroup/samplerepo/-/merge_requests/42",
				"last_commit": {"id": "head-sha-close"}
			},
			"project": {
				"id": 100,
				"path_with_namespace": "samplegroup/samplerepo",
				"web_url": "https://gitlab.example.com/samplegroup/samplerepo"
			},
			"user": {"username": "johndoe"}
		}`),
		"Merge Request Hook", "project",
	)
	return ev
}

func TestWebhookParityCreatePendingRunMatchesRunsService(t *testing.T) {
	sqlDB := setupParityDB(t)
	oldSvc := runs.NewService(parityLogger(), sqlDB)
	newSvc := NewService(oldSvc, nil)
	ev := makeOpenEvent("head-sha-parity-001")

	if err := newSvc.ProcessEventWithQuerier(context.Background(), db.New(sqlDB), ev, 0); err != nil {
		t.Fatalf("ProcessEventWithQuerier: %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}
	if run.Status != "pending" {
		t.Fatalf("run.Status = %q, want pending", run.Status)
	}
	if run.TriggerType != "webhook" {
		t.Fatalf("run.TriggerType = %q, want webhook", run.TriggerType)
	}
	if run.HeadSha != "head-sha-parity-001" {
		t.Fatalf("run.HeadSha = %q, want head-sha-parity-001", run.HeadSha)
	}
}

func TestWebhookParityUpdateSupersedesOlderActiveRun(t *testing.T) {
	sqlDB := setupParityDB(t)
	svc := NewService(runs.NewService(parityLogger(), sqlDB), nil)
	openEvent := makeOpenEvent("head-sha-open-001")
	updateEvent := makeUpdateEvent("head-sha-update-002")

	if err := svc.ProcessEventWithQuerier(context.Background(), db.New(sqlDB), openEvent, 0); err != nil {
		t.Fatalf("ProcessEventWithQuerier(open): %v", err)
	}
	if err := svc.ProcessEventWithQuerier(context.Background(), db.New(sqlDB), updateEvent, 0); err != nil {
		t.Fatalf("ProcessEventWithQuerier(update): %v", err)
	}

	queries := db.New(sqlDB)
	openRun, err := queries.GetReviewRunByIdempotencyKey(context.Background(), openEvent.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey(open): %v", err)
	}
	updateRun, err := queries.GetReviewRunByIdempotencyKey(context.Background(), updateEvent.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey(update): %v", err)
	}
	if updateRun.Status != "pending" {
		t.Fatalf("update run status = %q, want pending", updateRun.Status)
	}
	if openRun.Status != "cancelled" {
		t.Fatalf("open run status = %q, want cancelled", openRun.Status)
	}
	if openRun.ErrorCode != "superseded_by_new_head" {
		t.Fatalf("open run error_code = %q, want superseded_by_new_head", openRun.ErrorCode)
	}
	if !openRun.SupersededByRunID.Valid || openRun.SupersededByRunID.Int64 != updateRun.ID {
		t.Fatalf("open run superseded_by_run_id = %+v, want %d", openRun.SupersededByRunID, updateRun.ID)
	}
}

func TestWebhookParityCloseCancelsPendingRun(t *testing.T) {
	sqlDB := setupParityDB(t)
	svc := NewService(runs.NewService(parityLogger(), sqlDB), nil)
	openEvent := makeOpenEvent("head-sha-open-close-001")
	closeEvent := makeCloseEvent()

	if err := svc.ProcessEventWithQuerier(context.Background(), db.New(sqlDB), openEvent, 0); err != nil {
		t.Fatalf("ProcessEventWithQuerier(open): %v", err)
	}
	if err := svc.ProcessEventWithQuerier(context.Background(), db.New(sqlDB), closeEvent, 0); err != nil {
		t.Fatalf("ProcessEventWithQuerier(close): %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), openEvent.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey(open): %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", run.Status)
	}
}
