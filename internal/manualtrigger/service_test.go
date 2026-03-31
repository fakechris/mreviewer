package manualtrigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/scheduler"
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

func seedRunEntities(t *testing.T, sqlDB *sql.DB, projectGitlabID, mrIID int64, headSHA string) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	result, err := queries.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{
		Url:  "https://gitlab.example.com",
		Name: "GitLab",
	})
	if err != nil {
		t.Fatalf("UpsertGitlabInstance: %v", err)
	}
	instanceID, _ := result.LastInsertId()
	if instanceID == 0 {
		instance, getErr := queries.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
		if getErr != nil {
			t.Fatalf("GetGitlabInstanceByURL: %v", getErr)
		}
		instanceID = instance.ID
	}

	result, err = queries.UpsertProject(ctx, db.UpsertProjectParams{
		GitlabInstanceID:  instanceID,
		GitlabProjectID:   projectGitlabID,
		PathWithNamespace: "group/repo",
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	projectID, _ := result.LastInsertId()
	if projectID == 0 {
		project, getErr := queries.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
			GitlabInstanceID: instanceID,
			GitlabProjectID:  projectGitlabID,
		})
		if getErr != nil {
			t.Fatalf("GetProjectByGitlabID: %v", getErr)
		}
		projectID = project.ID
	}

	result, err = queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        mrIID,
		Title:        "Wait test MR",
		SourceBranch: "feature/wait",
		TargetBranch: "main",
		Author:       "alice",
		State:        "opened",
		IsDraft:      false,
		HeadSha:      headSHA,
		WebUrl:       "https://gitlab.example.com/group/repo/-/merge_requests/7",
	})
	if err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mrID, _ := result.LastInsertId()
	if mrID == 0 {
		mr, getErr := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
			ProjectID: projectID,
			MrIid:     mrIID,
		})
		if getErr != nil {
			t.Fatalf("GetMergeRequestByProjectMR: %v", getErr)
		}
		mrID = mr.ID
	}

	return instanceID, projectID, mrID
}

func TestTriggerCreatesPendingManualRun(t *testing.T) {
	sqlDB := setupTestDB(t)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v4/projects/123/merge_requests/7" {
			t.Fatalf("request path = %q, want %q", got, "/api/v4/projects/123/merge_requests/7")
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "test-token" {
			t.Fatalf("PRIVATE-TOKEN = %q, want %q", got, "test-token")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"id": 9001,
			"iid": 7,
			"project_id": 123,
			"title": "Manual trigger test",
			"description": "test mr",
			"state": "opened",
			"draft": false,
			"source_branch": "feature/manual",
			"target_branch": "main",
			"sha": "head-sha-123",
			"web_url": %q,
			"author": {"username": "alice"}
		}`, server.URL+"/group/subgroup/repo/-/merge_requests/7")
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	svc := NewService(testLogger(), sqlDB, client, server.URL, WithNow(func() time.Time { return now }))

	result, err := svc.Trigger(context.Background(), TriggerInput{ProjectID: 123, MRIID: 7})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	queries := db.New(sqlDB)

	run, err := queries.GetReviewRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "pending" {
		t.Fatalf("run status = %q, want %q", run.Status, "pending")
	}
	if run.TriggerType != "manual" {
		t.Fatalf("run trigger_type = %q, want %q", run.TriggerType, "manual")
	}
	if run.HeadSha != "head-sha-123" {
		t.Fatalf("run head_sha = %q, want %q", run.HeadSha, "head-sha-123")
	}

	instance, err := queries.GetGitlabInstanceByURL(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}

	project, err := queries.GetProjectByGitlabID(context.Background(), db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  123,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}
	if project.PathWithNamespace != "group/subgroup/repo" {
		t.Fatalf("project path = %q, want %q", project.PathWithNamespace, "group/subgroup/repo")
	}

	mr, err := queries.GetMergeRequestByProjectMR(context.Background(), db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     7,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	if mr.Title != "Manual trigger test" {
		t.Fatalf("mr title = %q, want %q", mr.Title, "Manual trigger test")
	}
	if mr.HeadSha != "head-sha-123" {
		t.Fatalf("mr head_sha = %q, want %q", mr.HeadSha, "head-sha-123")
	}
	if mr.WebUrl != server.URL+"/group/subgroup/repo/-/merge_requests/7" {
		t.Fatalf("mr web_url = %q, want %q", mr.WebUrl, server.URL+"/group/subgroup/repo/-/merge_requests/7")
	}
}

func TestTriggerStoresProviderRouteOverrideInScopeJSON(t *testing.T) {
	sqlDB := setupTestDB(t)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"id": 9003,
			"iid": 7,
			"project_id": 123,
			"title": "Manual trigger route override",
			"description": "test mr",
			"state": "opened",
			"draft": false,
			"source_branch": "feature/manual",
			"target_branch": "main",
			"sha": "head-sha-route",
			"web_url": %q,
			"author": {"username": "alice"}
		}`, server.URL+"/group/subgroup/repo/-/merge_requests/7")
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	svc := NewService(testLogger(), sqlDB, client, server.URL)
	result, err := svc.Trigger(context.Background(), TriggerInput{
		ProjectID:     123,
		MRIID:         7,
		ProviderRoute: "claude-opus-5-6",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	var scope struct {
		ProviderRoute string `json:"provider_route"`
	}
	if err := json.Unmarshal(run.ScopeJson, &scope); err != nil {
		t.Fatalf("unmarshal scope_json: %v", err)
	}
	if scope.ProviderRoute != "claude-opus-5-6" {
		t.Fatalf("scope provider_route = %q, want claude-opus-5-6", scope.ProviderRoute)
	}
}

func TestTriggerFailsWhenProjectPathCannotBeDerived(t *testing.T) {
	sqlDB := setupTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": 9002,
			"iid": 7,
			"project_id": 123,
			"title": "Broken web url",
			"state": "opened",
			"draft": false,
			"source_branch": "feature/manual",
			"target_branch": "main",
			"sha": "head-sha-456",
			"web_url": "https://invalid.example.com/not-a-merge-request-url",
			"author": {"username": "alice"}
		}`)
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	svc := NewService(testLogger(), sqlDB, client, server.URL)

	_, err = svc.Trigger(context.Background(), TriggerInput{ProjectID: 123, MRIID: 7})
	if err == nil {
		t.Fatal("Trigger error = nil, want non-nil")
	}
}

func TestWaitForTerminalRunReturnsCompletedRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedRunEntities(t, sqlDB, 101, 7, "head-sha-wait")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-wait",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "wait-run-completed",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	svc := NewService(testLogger(), sqlDB, nil, "https://gitlab.example.com", WithPollInterval(5*time.Millisecond))

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(20 * time.Millisecond)
		if updateErr := queries.UpdateReviewRunStatus(context.Background(), db.UpdateReviewRunStatusParams{
			Status:    "completed",
			ErrorCode: "",
			ID:        runID,
		}); updateErr != nil {
			t.Errorf("UpdateReviewRunStatus: %v", updateErr)
		}
	}()

	run, err := svc.WaitForTerminalRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("WaitForTerminalRun: %v", err)
	}
	<-done

	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}
}

func TestWaitForTerminalRunReturnsRequestedChangesRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedRunEntities(t, sqlDB, 101, 8, "head-sha-requested-changes")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-requested-changes",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "wait-run-requested-changes",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	svc := NewService(testLogger(), sqlDB, nil, "https://gitlab.example.com", WithPollInterval(5*time.Millisecond))

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(20 * time.Millisecond)
		if updateErr := queries.UpdateReviewRunStatus(context.Background(), db.UpdateReviewRunStatusParams{
			ID:        runID,
			Status:    "requested_changes",
			ErrorCode: "",
		}); updateErr != nil {
			t.Errorf("UpdateReviewRunStatus: %v", updateErr)
		}
	}()

	run, err := svc.WaitForTerminalRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("WaitForTerminalRun: %v", err)
	}
	<-done

	if run.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", run.Status)
	}
}

func TestWaitForTerminalRunHonorsContextDeadline(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedRunEntities(t, sqlDB, 102, 7, "head-sha-timeout")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-timeout",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "wait-run-timeout",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	svc := NewService(testLogger(), sqlDB, nil, "https://gitlab.example.com", WithPollInterval(50*time.Millisecond))

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = svc.WaitForTerminalRun(waitCtx, runID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForTerminalRun error = %v, want context deadline exceeded", err)
	}
}

type fakeRunProcessor struct {
	processFunc func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error)
	calls       int
	lastRunID   int64
}

func (f *fakeRunProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	f.calls++
	f.lastRunID = run.ID
	if f.processFunc != nil {
		return f.processFunc(ctx, run)
	}
	return scheduler.ProcessOutcome{}, nil
}

type fakeEventProcessor struct {
	processFunc func(context.Context, db.Querier, hooks.NormalizedEvent, int64) error
	calls       int
	lastEvent   hooks.NormalizedEvent
}

func (f *fakeEventProcessor) ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	f.calls++
	f.lastEvent = ev
	if f.processFunc != nil {
		return f.processFunc(ctx, q, ev, hookEventID)
	}
	return nil
}

func TestWaitForTerminalRunProcessesPendingRunViaProcessor(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedRunEntities(t, sqlDB, 201, 9, "head-sha-processor")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-processor",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "wait-run-processor",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	processor := &fakeRunProcessor{
		processFunc: func(_ context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
			if err := queries.UpdateReviewRunStatus(context.Background(), db.UpdateReviewRunStatusParams{
				ID:        run.ID,
				Status:    "requested_changes",
				ErrorCode: "",
			}); err != nil {
				t.Fatalf("UpdateReviewRunStatus: %v", err)
			}
			return scheduler.ProcessOutcome{Status: "requested_changes"}, nil
		},
	}

	svc := NewService(
		testLogger(),
		sqlDB,
		nil,
		"https://gitlab.example.com",
		WithPollInterval(5*time.Millisecond),
		WithRunProcessor(processor),
	)

	run, err := svc.WaitForTerminalRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("WaitForTerminalRun: %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if processor.lastRunID != runID {
		t.Fatalf("processor lastRunID = %d, want %d", processor.lastRunID, runID)
	}
	if run.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", run.Status)
	}
}

func TestTriggerUsesConfiguredEventProcessor(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"id": 9004,
			"iid": 7,
			"project_id": 123,
			"title": "Manual trigger event processor",
			"description": "test mr",
			"state": "opened",
			"draft": false,
			"source_branch": "feature/manual",
			"target_branch": "main",
			"sha": "head-sha-event-processor",
			"web_url": %q,
			"author": {"username": "alice"}
		}`, server.URL+"/group/subgroup/repo/-/merge_requests/7")
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	eventProcessor := &fakeEventProcessor{
		processFunc: func(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, _ int64) error {
			instResult, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{
				Url:  ev.GitLabInstanceURL,
				Name: "GitLab",
			})
			if err != nil {
				return err
			}
			instanceID, _ := instResult.LastInsertId()
			if instanceID == 0 {
				instance, err := q.GetGitlabInstanceByURL(ctx, ev.GitLabInstanceURL)
				if err != nil {
					return err
				}
				instanceID = instance.ID
			}
			projectResult, err := q.UpsertProject(ctx, db.UpsertProjectParams{
				GitlabInstanceID:  instanceID,
				GitlabProjectID:   ev.ProjectID,
				PathWithNamespace: ev.ProjectPath,
				Enabled:           true,
			})
			if err != nil {
				return err
			}
			projectID, _ := projectResult.LastInsertId()
			if projectID == 0 {
				project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
					GitlabInstanceID: instanceID,
					GitlabProjectID:  ev.ProjectID,
				})
				if err != nil {
					return err
				}
				projectID = project.ID
			}
			mrResult, err := q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
				ProjectID:    projectID,
				MrIid:        ev.MRIID,
				Title:        ev.Title,
				SourceBranch: ev.SourceBranch,
				TargetBranch: ev.TargetBranch,
				Author:       ev.Author,
				State:        ev.State,
				IsDraft:      ev.IsDraft,
				HeadSha:      ev.HeadSHA,
				WebUrl:       ev.WebURL,
			})
			if err != nil {
				return err
			}
			mrID, _ := mrResult.LastInsertId()
			if mrID == 0 {
				mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
					ProjectID: projectID,
					MrIid:     ev.MRIID,
				})
				if err != nil {
					return err
				}
				mrID = mr.ID
			}
			_, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{
				ProjectID:      projectID,
				MergeRequestID: mrID,
				TriggerType:    ev.TriggerType,
				HeadSha:        ev.HeadSHA,
				Status:         "pending",
				MaxRetries:     3,
				IdempotencyKey: ev.IdempotencyKey,
				ScopeJson:      db.NullRawMessage(ev.ScopeJSON),
			})
			return err
		},
	}

	svc := NewService(testLogger(), sqlDB, client, server.URL, WithEventProcessor(eventProcessor))
	result, err := svc.Trigger(ctx, TriggerInput{ProjectID: 123, MRIID: 7})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if eventProcessor.calls != 1 {
		t.Fatalf("event processor calls = %d, want 1", eventProcessor.calls)
	}
	if eventProcessor.lastEvent.TriggerType != "manual" {
		t.Fatalf("event trigger_type = %q, want manual", eventProcessor.lastEvent.TriggerType)
	}
	run, err := queries.GetReviewRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "pending" {
		t.Fatalf("run status = %q, want pending", run.Status)
	}
}

func TestTriggerPropagatesConfiguredEventProcessorError(t *testing.T) {
	sqlDB := setupTestDB(t)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"id": 9005,
			"iid": 7,
			"project_id": 123,
			"title": "Manual trigger event processor failure",
			"description": "test mr",
			"state": "opened",
			"draft": false,
			"source_branch": "feature/manual",
			"target_branch": "main",
			"sha": "head-sha-event-failure",
			"web_url": %q,
			"author": {"username": "alice"}
		}`, server.URL+"/group/subgroup/repo/-/merge_requests/7")
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	svc := NewService(testLogger(), sqlDB, client, server.URL, WithEventProcessor(&fakeEventProcessor{
		processFunc: func(context.Context, db.Querier, hooks.NormalizedEvent, int64) error {
			return errors.New("event processor boom")
		},
	}))

	_, err = svc.Trigger(context.Background(), TriggerInput{ProjectID: 123, MRIID: 7})
	if err == nil {
		t.Fatal("Trigger error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "event processor boom") {
		t.Fatalf("Trigger error = %q, want event processor boom", err.Error())
	}
}

func TestWaitForTerminalRunMarksRunFailedWhenProcessorErrors(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedRunEntities(t, sqlDB, 202, 10, "head-sha-processor-error")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-processor-error",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "wait-run-processor-error",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	svc := NewService(
		testLogger(),
		sqlDB,
		nil,
		"https://gitlab.example.com",
		WithPollInterval(5*time.Millisecond),
		WithRunProcessor(&fakeRunProcessor{
			processFunc: func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
				return scheduler.ProcessOutcome{}, errors.New("boom")
			},
		}),
	)

	run, err := svc.WaitForTerminalRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("WaitForTerminalRun: %v", err)
	}

	if run.Status != "failed" {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.ErrorCode != "manual_trigger_process_failed" {
		t.Fatalf("run error_code = %q, want manual_trigger_process_failed", run.ErrorCode)
	}
}
