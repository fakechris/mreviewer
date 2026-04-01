package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
)

type ActionStore interface {
	GetReviewRun(ctx context.Context, id int64) (db.ReviewRun, error)
	GetRunDetail(ctx context.Context, id int64) (db.GetRunDetailRow, error)
	InsertReviewRun(ctx context.Context, arg db.InsertReviewRunParams) (sql.Result, error)
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) (sql.Result, error)
	RetryReviewRunNow(ctx context.Context, id int64) error
	CancelReviewRun(ctx context.Context, id int64, errorCode, errorDetail string) error
	RequeueReviewRun(ctx context.Context, id int64) error
}

type ActionTxFunc func(ctx context.Context, store ActionStore) error

type actionTxRunner func(ctx context.Context, fn ActionTxFunc) error

type ActionError struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func WithActionTxRunner(runner func(ctx context.Context, fn ActionTxFunc) error) Option {
	return func(s *Service) {
		if runner != nil {
			s.actionTx = runner
		}
	}
}

func WithActionStoreFactory(sqlDB *sql.DB, newStore func(db.DBTX) db.Store) Option {
	return func(s *Service) {
		if sqlDB == nil || newStore == nil {
			return
		}
		s.actionTx = func(ctx context.Context, fn ActionTxFunc) error {
			return db.RunTxWithStore(ctx, sqlDB, newStore, func(ctx context.Context, store db.Store) error {
				return fn(ctx, store)
			})
		}
	}
}

func (s *Service) RetryRun(ctx context.Context, runID int64, actor string) (RunDetail, error) {
	return s.runAction(ctx, runID, actor, "retry_run", func(ctx context.Context, store ActionStore, run db.ReviewRun) (int64, error) {
		if run.Status != "failed" || run.SupersededByRunID.Valid {
			return 0, &ActionError{StatusCode: 409, Message: "run is not retryable"}
		}
		if err := store.RetryReviewRunNow(ctx, runID); err != nil {
			return 0, err
		}
		return runID, nil
	})
}

func (s *Service) CancelRun(ctx context.Context, runID int64, actor string) (RunDetail, error) {
	return s.runAction(ctx, runID, actor, "cancel_run", func(ctx context.Context, store ActionStore, run db.ReviewRun) (int64, error) {
		switch run.Status {
		case "pending", "running":
		case "failed":
			if !run.NextRetryAt.Valid {
				return 0, &ActionError{StatusCode: 409, Message: "run is not cancellable"}
			}
		default:
			return 0, &ActionError{StatusCode: 409, Message: "run is not cancellable"}
		}
		if err := store.CancelReviewRun(ctx, runID, "cancelled_by_operator", "Cancelled by admin operator"); err != nil {
			return 0, err
		}
		return runID, nil
	})
}

func (s *Service) RequeueRun(ctx context.Context, runID int64, actor string) (RunDetail, error) {
	return s.runAction(ctx, runID, actor, "requeue_run", func(ctx context.Context, store ActionStore, run db.ReviewRun) (int64, error) {
		if run.SupersededByRunID.Valid || run.ErrorCode == "superseded_by_new_head" {
			return 0, &ActionError{StatusCode: 409, Message: "superseded runs cannot be requeued"}
		}
		switch run.Status {
		case "cancelled", "failed":
		default:
			return 0, &ActionError{StatusCode: 409, Message: "run is not requeueable"}
		}
		if err := store.RequeueReviewRun(ctx, runID); err != nil {
			return 0, err
		}
		return runID, nil
	})
}

func (s *Service) RerunRun(ctx context.Context, runID int64, actor string) (RunDetail, error) {
	return s.runAction(ctx, runID, actor, "rerun_run", func(ctx context.Context, store ActionStore, run db.ReviewRun) (int64, error) {
		switch run.Status {
		case "completed", "failed", "cancelled":
		default:
			return 0, &ActionError{StatusCode: 409, Message: "run is not rerunnable"}
		}
		idempotencyKey := fmt.Sprintf("admin-rerun:%d:%d", run.ID, s.now().UTC().UnixNano())
		res, err := store.InsertReviewRun(ctx, db.InsertReviewRunParams{
			ProjectID:      run.ProjectID,
			MergeRequestID: run.MergeRequestID,
			HookEventID:    run.HookEventID,
			TriggerType:    run.TriggerType,
			HeadSha:        run.HeadSha,
			Status:         "pending",
			MaxRetries:     run.MaxRetries,
			IdempotencyKey: idempotencyKey,
			ScopeJson:      run.ScopeJson,
		})
		if err != nil {
			return 0, err
		}
		newRunID, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		return newRunID, nil
	})
}

func (s *Service) runAction(ctx context.Context, runID int64, actor, action string, mutate func(ctx context.Context, store ActionStore, run db.ReviewRun) (int64, error)) (RunDetail, error) {
	if s == nil || s.actionTx == nil {
		return RunDetail{}, fmt.Errorf("adminapi: action transactions are not configured")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "admin"
	}

	var snapshot RunDetail
	err := s.actionTx(ctx, func(ctx context.Context, store ActionStore) error {
		run, err := store.GetReviewRun(ctx, runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &ActionError{StatusCode: 404, Message: "run not found"}
			}
			return err
		}
		targetRunID, err := mutate(ctx, store, run)
		if err != nil {
			return err
		}
		detail, err := store.GetRunDetail(ctx, targetRunID)
		if err != nil {
			return err
		}
		if _, err := store.InsertAuditLog(ctx, db.InsertAuditLogParams{
			EntityType: "review_run",
			EntityID:   targetRunID,
			Action:     action,
			Actor:      actor,
			Detail:     mustMarshalActionDetail(runID, targetRunID),
		}); err != nil {
			return err
		}
		snapshot = RunDetail{
			RunListItem: buildRunListItem(s.now(), db.ListRecentRunsRow{
				ID:                      detail.ID,
				Platform:                detail.Platform,
				ProjectPath:             detail.ProjectPath,
				WebUrl:                  detail.WebUrl,
				MergeRequestID:          detail.MergeRequestID,
				Status:                  detail.Status,
				ErrorCode:               detail.ErrorCode,
				TriggerType:             detail.TriggerType,
				HeadSha:                 detail.HeadSha,
				ClaimedBy:               detail.ClaimedBy,
				RetryCount:              detail.RetryCount,
				NextRetryAt:             detail.NextRetryAt,
				ProviderLatencyMs:       detail.ProviderLatencyMs,
				ProviderTokensTotal:     detail.ProviderTokensTotal,
				HookAction:              detail.HookAction,
				HookVerificationOutcome: detail.HookVerificationOutcome,
				FindingCount:            detail.FindingCount,
				CommentActionCount:      detail.CommentActionCount,
				CreatedAt:               detail.CreatedAt,
				UpdatedAt:               detail.UpdatedAt,
				StartedAt:               detail.StartedAt,
				CompletedAt:             detail.CompletedAt,
			}),
			HookEventID:       detail.HookEventID,
			ErrorDetail:       detail.ErrorDetail,
			MaxRetries:        detail.MaxRetries,
			IdempotencyKey:    detail.IdempotencyKey,
			ScopeJSON:         detail.ScopeJson,
			SupersededByRunID: detail.SupersededByRunID,
		}
		return nil
	})
	return snapshot, err
}

func mustMarshalActionDetail(sourceRunID, targetRunID int64) json.RawMessage {
	payload, _ := json.Marshal(map[string]int64{
		"source_run_id": sourceRunID,
		"target_run_id": targetRunID,
	})
	return payload
}
