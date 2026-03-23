package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/metrics"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

const (
	defaultPollInterval    = time.Second
	defaultRetryBaseDelay  = time.Second
	defaultRetryMaxDelay   = 30 * time.Second
	defaultClaimRetryWait  = 25 * time.Millisecond
	defaultClaimRetryCount = 8
	defaultFailureCode     = "run_failed"
	defaultClaimTimeoutMin = 10
	defaultReaperInterval  = 60 * time.Second
)

var ErrNoClaimableRuns = errors.New("scheduler: no claimable runs")

var errRunCancelled = errors.New("scheduler: run cancelled")

type Processor interface {
	ProcessRun(ctx context.Context, run db.ReviewRun) (ProcessOutcome, error)
}

type ProcessOutcome struct {
	Status              string
	ProviderLatencyMs   int64
	ProviderTokensTotal int64
	ReviewFindings      []db.ReviewFinding
}

type FuncProcessor func(ctx context.Context, run db.ReviewRun) (ProcessOutcome, error)

func (f FuncProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (ProcessOutcome, error) {
	return f(ctx, run)
}

type RunError struct {
	code      string
	retryable bool
	err       error
}

func (e *RunError) Error() string {
	if e == nil {
		return ""
	}
	if e.err != nil {
		return e.err.Error()
	}
	if e.code != "" {
		return e.code
	}
	return defaultFailureCode
}

func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func NewRetryableError(code string, err error) error {
	return &RunError{code: code, retryable: true, err: err}
}

func NewTerminalError(code string, err error) error {
	return &RunError{code: code, retryable: false, err: err}
}

type Option func(*Service)

type Service struct {
	logger              *slog.Logger
	db                  *sql.DB
	processor           Processor
	gateService         *gate.Service
	workerID            string
	pollInterval        time.Duration
	retryBaseDelay      time.Duration
	retryMaxDelay       time.Duration
	claimTimeoutMinutes int
	reaperInterval      time.Duration
	now                 func() time.Time
	metrics             *metrics.Registry
	tracer              *tracing.Recorder
}

func NewService(logger *slog.Logger, database *sql.DB, processor Processor, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	svc := &Service{
		logger:              logger,
		db:                  database,
		processor:           processor,
		workerID:            defaultWorkerID(),
		pollInterval:        defaultPollInterval,
		retryBaseDelay:      defaultRetryBaseDelay,
		retryMaxDelay:       defaultRetryMaxDelay,
		claimTimeoutMinutes: defaultClaimTimeoutMin,
		reaperInterval:      defaultReaperInterval,
		now:                 time.Now,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}

	if svc.retryBaseDelay <= 0 {
		svc.retryBaseDelay = defaultRetryBaseDelay
	}
	if svc.retryMaxDelay < svc.retryBaseDelay {
		svc.retryMaxDelay = svc.retryBaseDelay
	}
	if svc.pollInterval <= 0 {
		svc.pollInterval = defaultPollInterval
	}
	if svc.claimTimeoutMinutes <= 0 {
		svc.claimTimeoutMinutes = defaultClaimTimeoutMin
	}
	if svc.reaperInterval <= 0 {
		svc.reaperInterval = defaultReaperInterval
	}

	return svc
}

func WithWorkerID(workerID string) Option {
	return func(s *Service) {
		if workerID != "" {
			s.workerID = workerID
		}
	}
}

func WithPollInterval(interval time.Duration) Option {
	return func(s *Service) {
		if interval > 0 {
			s.pollInterval = interval
		}
	}
}

func WithRetryBaseDelay(delay time.Duration) Option {
	return func(s *Service) {
		if delay > 0 {
			s.retryBaseDelay = delay
		}
	}
}

func WithRetryMaxDelay(delay time.Duration) Option {
	return func(s *Service) {
		if delay > 0 {
			s.retryMaxDelay = delay
		}
	}
}

func WithMetrics(registry *metrics.Registry) Option {
	return func(s *Service) { s.metrics = registry }
}

func WithTracer(recorder *tracing.Recorder) Option {
	return func(s *Service) { s.tracer = recorder }
}

func WithGateService(gateSvc *gate.Service) Option {
	return func(s *Service) { s.gateService = gateSvc }
}

func WithClaimTimeout(minutes int) Option {
	return func(s *Service) {
		if minutes > 0 {
			s.claimTimeoutMinutes = minutes
		}
	}
}

func WithReaperInterval(interval time.Duration) Option {
	return func(s *Service) {
		if interval > 0 {
			s.reaperInterval = interval
		}
	}
}

func NewNoopProcessor(logger *slog.Logger) Processor {
	return FuncProcessor(func(ctx context.Context, run db.ReviewRun) (ProcessOutcome, error) {
		if logger != nil {
			logger.InfoContext(ctx, "scheduler processed run with noop processor",
				"run_id", run.ID,
				"merge_request_id", run.MergeRequestID,
				"retry_count", run.RetryCount,
			)
		}
		return ProcessOutcome{}, nil
	})
}

func (s *Service) Run(ctx context.Context) error {
	if s.processor == nil {
		return fmt.Errorf("scheduler: processor is required")
	}

	go s.startReaper(ctx)

	if _, err := s.RunOnce(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) (int, error) {
	if s.processor == nil {
		return 0, fmt.Errorf("scheduler: processor is required")
	}

	run, err := s.ClaimNextRun(ctx)
	if err != nil {
		if errors.Is(err, ErrNoClaimableRuns) {
			return 0, nil
		}
		return 0, err
	}

	if err := s.processClaimedRun(ctx, *run); err != nil {
		return 1, err
	}

	return 1, nil
}

func (s *Service) ClaimNextRun(ctx context.Context) (*db.ReviewRun, error) {
	if s.db == nil {
		return nil, fmt.Errorf("scheduler: database is required")
	}

	for attempt := 0; attempt < defaultClaimRetryCount; attempt++ {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return nil, fmt.Errorf("scheduler: begin claim tx: %w", err)
		}

		q := db.New(tx)
		run, err := q.GetNextClaimableReviewRun(ctx)
		if err != nil {
			_ = tx.Rollback()
			if errors.Is(err, sql.ErrNoRows) {
				if attempt < defaultClaimRetryCount-1 {
					if err := sleepContext(ctx, defaultClaimRetryWait); err != nil {
						return nil, err
					}
					continue
				}
				return nil, ErrNoClaimableRuns
			}
			return nil, fmt.Errorf("scheduler: select claimable run: %w", err)
		}

		if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{
			ClaimedBy: s.workerID,
			ID:        run.ID,
		}); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("scheduler: claim run %d: %w", run.ID, err)
		}

		claimedRun, err := q.GetReviewRun(ctx, run.ID)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("scheduler: reload claimed run %d: %w", run.ID, err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("scheduler: commit claim tx: %w", err)
		}

		s.logger.InfoContext(ctx, "claimed review run",
			"run_id", claimedRun.ID,
			"worker_id", s.workerID,
			"merge_request_id", claimedRun.MergeRequestID,
			"project_id", claimedRun.ProjectID,
			"status", claimedRun.Status,
			"retry_count", claimedRun.RetryCount,
		)

		return &claimedRun, nil
	}

	return nil, ErrNoClaimableRuns
}

// ReapStaleRuns resets running review runs that have exceeded the claim
// timeout back to failed with a retry scheduled, allowing them to be
// reclaimed by another worker.
func (s *Service) ReapStaleRuns(ctx context.Context) (int64, error) {
	reaped, err := db.New(s.db).ReapStaleRunningRuns(ctx, s.claimTimeoutMinutes)
	if err != nil {
		return 0, fmt.Errorf("scheduler: reap stale runs: %w", err)
	}
	if reaped > 0 {
		s.logger.InfoContext(ctx, "reaped stale running runs",
			"count", reaped,
			"claim_timeout_minutes", s.claimTimeoutMinutes,
		)
	}
	return reaped, nil
}

func (s *Service) startReaper(ctx context.Context) {
	ticker := time.NewTicker(s.reaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ReapStaleRuns(ctx); err != nil {
				s.logger.ErrorContext(ctx, "reaper failed", "error", err)
			}
		}
	}
}

func (s *Service) processClaimedRun(ctx context.Context, run db.ReviewRun) error {
	ctx, endSpan := s.startSpan(ctx, "run.process", map[string]string{"run_id": fmt.Sprintf("%d", run.ID)})
	defer endSpan()
	s.incrementRunStart(run)
	run, err := s.reloadRunningRun(ctx, run.ID)
	if err != nil {
		if errors.Is(err, errRunCancelled) {
			return nil
		}
		return err
	}

	outcome, err := s.processor.ProcessRun(ctx, run)
	if err == nil {
		status := outcome.Status
		if status == "" {
			status = "completed"
		}
		if status == "parser_error" {
			currentRun, err := db.New(s.db).GetReviewRun(ctx, run.ID)
			if err != nil {
				return fmt.Errorf("scheduler: reload run %d before parser_error completion: %w", run.ID, err)
			}
			if currentRun.Status == "cancelled" {
				return nil
			}
			if currentRun.Status != "parser_error" {
				return fmt.Errorf("scheduler: run %d expected parser_error status, got %q", run.ID, currentRun.Status)
			}
			if err := db.New(s.db).UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: status, ErrorCode: "", ErrorDetail: sql.NullString{}, ID: run.ID}); err != nil {
				return fmt.Errorf("scheduler: mark run %d parser_error: %w", run.ID, err)
			}
			if err := s.persistProviderMetricsIfNeeded(ctx, run.ID, currentRun, outcome); err != nil {
				return err
			}
			s.logger.InfoContext(ctx, "completed review run with parser_error result",
				"run_id", run.ID,
				"worker_id", s.workerID,
				"merge_request_id", run.MergeRequestID,
				"retry_count", run.RetryCount,
			)
			return nil
		}
		if err := s.publishGateResult(ctx, run, outcome); err != nil {
			return err
		}
		updated, err := db.New(s.db).UpdateReviewRunCompletedIfRunning(ctx, db.UpdateReviewRunCompletedParams{
			ProviderLatencyMs:   outcome.ProviderLatencyMs,
			ProviderTokensTotal: outcome.ProviderTokensTotal,
			ID:                  run.ID,
		})
		if err != nil {
			return fmt.Errorf("scheduler: mark run %d completed: %w", run.ID, err)
		}
		if !updated {
			currentRun, err := s.handleSkippedTerminalWrite(ctx, run, "completed")
			if err != nil {
				return err
			}
			if currentRun.Status != "cancelled" {
				if err := s.persistProviderMetricsIfNeeded(ctx, run.ID, currentRun, outcome); err != nil {
					return err
				}
				s.logger.InfoContext(ctx, "completed review run",
					"run_id", run.ID,
					"worker_id", s.workerID,
					"merge_request_id", run.MergeRequestID,
					"retry_count", run.RetryCount,
					"status_managed_by_processor", true,
				)
				s.recordTerminalMetrics("completed", run, "")
			}
			return nil
		}

		s.logger.InfoContext(ctx, "completed review run",
			"run_id", run.ID,
			"worker_id", s.workerID,
			"merge_request_id", run.MergeRequestID,
			"retry_count", run.RetryCount,
		)
		s.recordTerminalMetrics("completed", run, "")
		return nil
	}

	code, detail, retryable := classifyRunError(err)
	if retryable && run.RetryCount < run.MaxRetries {
		nextRetryCount := run.RetryCount + 1
		delay := s.retryDelay(run.RetryCount)
		nextRetryAt := s.now().Add(delay)

		updated, err := db.New(s.db).MarkReviewRunRetryableFailureIfRunning(ctx, db.MarkReviewRunRetryableFailureParams{
			ErrorCode:   code,
			ErrorDetail: nullableString(detail),
			RetryCount:  nextRetryCount,
			NextRetryAt: sql.NullTime{Time: nextRetryAt, Valid: true},
			ID:          run.ID,
		})
		if err != nil {
			return fmt.Errorf("scheduler: mark run %d retryable failure: %w", run.ID, err)
		}
		if !updated {
			currentRun, err := s.handleSkippedTerminalWrite(ctx, run, "retryable failure")
			if err != nil {
				return err
			}
			if currentRun.Status != "cancelled" {
				s.logger.WarnContext(ctx, "review run already marked failed with retry scheduled",
					"run_id", run.ID,
					"worker_id", s.workerID,
					"merge_request_id", run.MergeRequestID,
					"error_code", code,
					"retry_count", currentRun.RetryCount,
					"next_retry_at", currentRun.NextRetryAt.Time.UTC().Format(time.RFC3339Nano),
					"status_managed_by_processor", true,
				)
				s.recordTerminalMetrics("failed", run, code)
			}
			return nil
		}

		s.logger.WarnContext(ctx, "review run failed with retry scheduled",
			"run_id", run.ID,
			"worker_id", s.workerID,
			"merge_request_id", run.MergeRequestID,
			"error_code", code,
			"retry_count", nextRetryCount,
			"next_retry_at", nextRetryAt.UTC().Format(time.RFC3339Nano),
		)
		s.recordTerminalMetrics("failed", run, code)
		return nil
	}

	updated, err := db.New(s.db).MarkReviewRunFailedIfRunning(ctx, db.MarkReviewRunFailedParams{
		ErrorCode:   code,
		ErrorDetail: nullableString(detail),
		RetryCount:  run.RetryCount,
		ID:          run.ID,
	})
	if err != nil {
		return fmt.Errorf("scheduler: mark run %d failed: %w", run.ID, err)
	}
	if !updated {
		currentRun, err := s.handleSkippedTerminalWrite(ctx, run, "failed")
		if err != nil {
			return err
		}
		if currentRun.Status != "cancelled" {
			s.logger.ErrorContext(ctx, "review run failed permanently",
				"run_id", run.ID,
				"worker_id", s.workerID,
				"merge_request_id", run.MergeRequestID,
				"error_code", code,
				"retry_count", run.RetryCount,
				"status_managed_by_processor", true,
			)
			s.recordTerminalMetrics("failed", run, code)
		}
		return nil
	}

	s.logger.ErrorContext(ctx, "review run failed permanently",
		"run_id", run.ID,
		"worker_id", s.workerID,
		"merge_request_id", run.MergeRequestID,
		"error_code", code,
		"retry_count", run.RetryCount,
	)
	s.recordTerminalMetrics("failed", run, code)
	return nil
}

func (s *Service) publishGateResult(ctx context.Context, run db.ReviewRun, outcome ProcessOutcome) error {
	if s.gateService == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(outcome.Status), "completed") {
		return nil
	}
	queries := db.New(s.db)
	var policyPtr *db.ProjectPolicy
	policy, err := queries.GetProjectPolicy(ctx, run.ProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("scheduler: load gate policy for run %d: %w", run.ID, err)
	}
	if err == nil {
		policyPtr = &policy
	}
	findings := outcome.ReviewFindings
	if findings == nil {
		findings, err = queries.ListFindingsByRun(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("scheduler: load findings for gate publication on run %d: %w", run.ID, err)
		}
	}
	result := gate.ComputeResult(run, policyPtr, findings, tracing.CurrentTraceID(ctx))
	if err := s.gateService.Publish(ctx, result); err != nil {
		return fmt.Errorf("scheduler: publish gate result for run %d: %w", run.ID, err)
	}
	return nil
}

func (s *Service) incrementRunStart(run db.ReviewRun) {
	if s.metrics == nil {
		return
	}
	s.metrics.IncCounter("review_run_started_total", map[string]string{"trigger_type": run.TriggerType})
}

func (s *Service) recordTerminalMetrics(status string, run db.ReviewRun, errorCode string) {
	if s.metrics == nil {
		return
	}
	labels := map[string]string{"trigger_type": run.TriggerType}
	if errorCode != "" {
		labels["error_code"] = errorCode
	}
	if status == "completed" {
		s.metrics.IncCounter("review_run_completed_total", labels)
		return
	}
	if status == "failed" {
		s.metrics.IncCounter("review_run_failed_total", labels)
	}
}

func (s *Service) startSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	if s.tracer == nil {
		return ctx, func() {}
	}
	return s.tracer.Start(ctx, name, attrs)
}

func (s *Service) handleSkippedTerminalWrite(ctx context.Context, run db.ReviewRun, terminalState string) (db.ReviewRun, error) {
	currentRun, err := db.New(s.db).GetReviewRun(ctx, run.ID)
	if err != nil {
		return db.ReviewRun{}, fmt.Errorf("scheduler: reload run %d after skipped %s write: %w", run.ID, terminalState, err)
	}

	if currentRun.Status == "cancelled" {
		s.logger.InfoContext(ctx, "skipped terminal write for cancelled review run",
			"run_id", run.ID,
			"worker_id", s.workerID,
			"merge_request_id", currentRun.MergeRequestID,
			"terminal_state", terminalState,
		)
		return currentRun, nil
	}

	switch terminalState {
	case "completed":
		if currentRun.Status == "completed" || currentRun.Status == "requested_changes" {
			return currentRun, nil
		}
	case "failed":
		if currentRun.Status == "failed" {
			return currentRun, nil
		}
	case "retryable failure":
		if currentRun.Status == "failed" && currentRun.NextRetryAt.Valid {
			return currentRun, nil
		}
	}

	return db.ReviewRun{}, fmt.Errorf("scheduler: skipped %s write for run %d with unexpected status %q", terminalState, run.ID, currentRun.Status)
}

func (s *Service) persistProviderMetricsIfNeeded(ctx context.Context, runID int64, currentRun db.ReviewRun, outcome ProcessOutcome) error {
	if outcome.ProviderLatencyMs == 0 && outcome.ProviderTokensTotal == 0 {
		return nil
	}
	if currentRun.ProviderLatencyMs == outcome.ProviderLatencyMs && currentRun.ProviderTokensTotal == outcome.ProviderTokensTotal {
		return nil
	}
	if err := db.New(s.db).UpdateReviewRunProviderMetrics(ctx, db.UpdateReviewRunProviderMetricsParams{
		ProviderLatencyMs:   outcome.ProviderLatencyMs,
		ProviderTokensTotal: outcome.ProviderTokensTotal,
		ID:                  runID,
	}); err != nil {
		return fmt.Errorf("scheduler: persist provider metrics for run %d: %w", runID, err)
	}
	return nil
}

func (s *Service) retryDelay(retryCount int32) time.Duration {
	delay := s.retryBaseDelay
	for i := int32(0); i < retryCount; i++ {
		if delay >= s.retryMaxDelay/2 {
			return s.retryMaxDelay
		}
		delay *= 2
	}
	if delay > s.retryMaxDelay {
		return s.retryMaxDelay
	}
	return delay
}

func classifyRunError(err error) (code string, detail string, retryable bool) {
	if err == nil {
		return "", "", false
	}

	var runErr *RunError
	if errors.As(err, &runErr) {
		code = runErr.code
		retryable = runErr.retryable
	} else {
		code = defaultFailureCode
	}

	if code == "" {
		code = defaultFailureCode
	}
	detail = err.Error()
	return code, detail, retryable
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "worker"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}

func (s *Service) reloadRunningRun(ctx context.Context, runID int64) (db.ReviewRun, error) {
	run, err := db.New(s.db).GetReviewRun(ctx, runID)
	if err != nil {
		return db.ReviewRun{}, fmt.Errorf("scheduler: reload run %d before processing: %w", runID, err)
	}

	if run.Status == "cancelled" {
		s.logger.InfoContext(ctx, "skipping cancelled review run before processing",
			"run_id", run.ID,
			"worker_id", s.workerID,
			"merge_request_id", run.MergeRequestID,
		)
		return db.ReviewRun{}, errRunCancelled
	}

	if run.Status != "running" {
		return db.ReviewRun{}, fmt.Errorf("scheduler: run %d has unexpected status %q before processing", runID, run.Status)
	}

	return run, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
