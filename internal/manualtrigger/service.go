package manualtrigger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/hooks"
	runsvc "github.com/mreviewer/mreviewer/internal/runs"
)

type MergeRequestReader interface {
	GetMergeRequest(ctx context.Context, projectID, mergeRequestIID int64) (gitlab.MergeRequest, error)
}

type Option func(*Service)

type Service struct {
	logger   *slog.Logger
	db       *sql.DB
	gitlab   MergeRequestReader
	baseURL  string
	now      func() time.Time
	poll     time.Duration
	newStore func(db.DBTX) db.Store
}

type TriggerInput struct {
	ProjectID     int64
	MRIID         int64
	ProviderRoute string
}

type TriggerResult struct {
	RunID          int64
	ProjectID      int64
	MRIID          int64
	HeadSHA        string
	IdempotencyKey string
}

func NewService(logger *slog.Logger, sqlDB *sql.DB, client MergeRequestReader, baseURL string, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	svc := &Service{
		logger:   logger,
		db:       sqlDB,
		gitlab:   client,
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		now:      time.Now,
		poll:     time.Second,
		newStore: defaultManualNewStore,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}

	if svc.now == nil {
		svc.now = time.Now
	}
	if svc.poll <= 0 {
		svc.poll = time.Second
	}

	return svc
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		s.now = now
	}
}

func WithPollInterval(interval time.Duration) Option {
	return func(s *Service) {
		s.poll = interval
	}
}

func WithStoreFactory(fn func(db.DBTX) db.Store) Option {
	return func(s *Service) {
		s.newStore = fn
	}
}

func defaultManualNewStore(conn db.DBTX) db.Store { return db.New(conn) }

func (s *Service) Trigger(ctx context.Context, input TriggerInput) (TriggerResult, error) {
	if s.db == nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: database is required")
	}
	if s.gitlab == nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: gitlab client is required")
	}
	if s.baseURL == "" {
		return TriggerResult{}, fmt.Errorf("manual trigger: base URL is required")
	}
	if input.ProjectID <= 0 {
		return TriggerResult{}, fmt.Errorf("manual trigger: project_id must be greater than zero")
	}
	if input.MRIID <= 0 {
		return TriggerResult{}, fmt.Errorf("manual trigger: mr_iid must be greater than zero")
	}

	mr, err := s.gitlab.GetMergeRequest(ctx, input.ProjectID, input.MRIID)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: get merge request: %w", err)
	}

	projectID := input.ProjectID
	if mr.ProjectID > 0 {
		if mr.ProjectID != input.ProjectID {
			return TriggerResult{}, fmt.Errorf("manual trigger: merge request project_id mismatch: got %d want %d", mr.ProjectID, input.ProjectID)
		}
		projectID = mr.ProjectID
	}

	mrIID := input.MRIID
	if mr.IID > 0 {
		if mr.IID != input.MRIID {
			return TriggerResult{}, fmt.Errorf("manual trigger: merge request iid mismatch: got %d want %d", mr.IID, input.MRIID)
		}
		mrIID = mr.IID
	}

	projectPath, err := projectPathFromMRURL(mr.WebURL)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: derive project path: %w", err)
	}

	ev := hooks.NormalizedEvent{
		GitLabInstanceURL: s.baseURL,
		ProjectID:         projectID,
		ProjectPath:       projectPath,
		MRIID:             mrIID,
		Action:            "manual_trigger",
		HeadSHA:           mr.HeadSHA,
		HeadSHADeferred:   strings.TrimSpace(mr.HeadSHA) == "",
		IsDraft:           mr.Draft,
		HookSource:        "manual",
		TriggerType:       "manual",
		EventType:         "manual_trigger",
		IdempotencyKey:    computeManualIdempotencyKey(s.baseURL, projectID, mrIID, mr.HeadSHA, s.now()),
		Title:             mr.Title,
		SourceBranch:      mr.SourceBranch,
		TargetBranch:      mr.TargetBranch,
		Author:            mr.Author.Username,
		WebURL:            mr.WebURL,
		State:             mr.State,
	}
	if scopeJSON := buildManualRunScope(input.ProviderRoute); len(scopeJSON) > 0 {
		ev.ScopeJSON = scopeJSON
	}

	if err := runsvc.NewService(s.logger, s.db, runsvc.WithStoreFactory(s.newStore)).ProcessEvent(ctx, ev, 0); err != nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: create review run: %w", err)
	}

	run, err := s.newStore(s.db).GetReviewRunByIdempotencyKey(ctx, ev.IdempotencyKey)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("manual trigger: load created review run: %w", err)
	}

	return TriggerResult{
		RunID:          run.ID,
		ProjectID:      projectID,
		MRIID:          mrIID,
		HeadSHA:        mr.HeadSHA,
		IdempotencyKey: ev.IdempotencyKey,
	}, nil
}

func (s *Service) WaitForTerminalRun(ctx context.Context, runID int64) (db.ReviewRun, error) {
	if s.db == nil {
		return db.ReviewRun{}, fmt.Errorf("manual trigger: database is required")
	}
	if runID <= 0 {
		return db.ReviewRun{}, fmt.Errorf("manual trigger: run_id must be greater than zero")
	}

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	for {
		run, err := s.newStore(s.db).GetReviewRun(ctx, runID)
		if err != nil {
			return db.ReviewRun{}, fmt.Errorf("manual trigger: load review run %d: %w", runID, err)
		}
		if isTerminalStatus(run.Status) {
			return run, nil
		}

		select {
		case <-ctx.Done():
			return db.ReviewRun{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "requested_changes", "failed", "cancelled", "parser_error":
		return true
	default:
		return false
	}
}

func projectPathFromMRURL(webURL string) (string, error) {
	if strings.TrimSpace(webURL) == "" {
		return "", fmt.Errorf("empty web_url")
	}

	parsed, err := url.Parse(webURL)
	if err != nil {
		return "", fmt.Errorf("parse web_url: %w", err)
	}

	parts := strings.SplitN(parsed.Path, "/-/merge_requests/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("web_url does not look like a merge request URL: %s", webURL)
	}

	projectPath := strings.Trim(parts[0], "/")
	if projectPath == "" {
		return "", fmt.Errorf("project path missing in web_url: %s", webURL)
	}

	return projectPath, nil
}

func computeManualIdempotencyKey(baseURL string, projectID, mrIID int64, headSHA string, now time.Time) string {
	payload := fmt.Sprintf("manual|%s|%d|%d|%s|%d",
		strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		projectID,
		mrIID,
		strings.TrimSpace(headSHA),
		now.UTC().UnixNano(),
	)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:16])
}

func buildManualRunScope(providerRoute string) json.RawMessage {
	providerRoute = strings.TrimSpace(providerRoute)
	if providerRoute == "" {
		return nil
	}
	scopeJSON, err := json.Marshal(map[string]any{
		"provider_route": providerRoute,
	})
	if err != nil {
		slog.Warn("manual trigger: build scope_json failed", "provider_route", providerRoute, "error", err)
		return nil
	}
	return scopeJSON
}
