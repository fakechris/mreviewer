package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/commands"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/runs"
	"github.com/mreviewer/mreviewer/internal/writer"
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

func TestServiceLevelDatabaseFailureRecovery(t *testing.T) {
	t.Run("webhook write rollback and retry recovery", func(t *testing.T) {
		sqlDB := setupTestDB(t)
		logger := testLogger()
		runSvc := runs.NewService(logger, sqlDB)
		handler := hooks.NewHandler(logger, sqlDB, "test-secret", runSvc)

		failDelivery := "service-db-fail-webhook"
		body := []byte(`{
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
		}`)

		closeTestDBConnection(t, sqlDB)
		failResp := postWebhookRequest(t, handler, failDelivery, "Merge Request Hook", body)
		if failResp.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 during DB outage, got %d: %s", failResp.Code, failResp.Body.String())
		}

		reopenedDB := setupTestDB(t)
		defer reopenedDB.Close()
		queries := db.New(reopenedDB)
		assertNoHookEvent(t, queries, failDelivery)
		assertNoReviewRunByKey(t, reopenedDB, normalizedRunKey("https://gitlab.example.com", 42, 7, "abc123def456", "webhook"))

		handler = hooks.NewHandler(logger, reopenedDB, "test-secret", runs.NewService(logger, reopenedDB))
		successResp := postWebhookRequest(t, handler, failDelivery, "Merge Request Hook", body)
		if successResp.Code != http.StatusOK {
			t.Fatalf("expected 200 after DB recovery, got %d: %s", successResp.Code, successResp.Body.String())
		}

		hookEvent, err := queries.GetHookEventByDeliveryKey(context.Background(), failDelivery)
		if err != nil {
			t.Fatalf("GetHookEventByDeliveryKey: %v", err)
		}
		if hookEvent.ID == 0 {
			t.Fatal("expected persisted hook_event after retry")
		}

		run, err := queries.GetReviewRunByIdempotencyKey(context.Background(), normalizedRunKey("https://gitlab.example.com", 42, 7, "abc123def456", "webhook"))
		if err != nil {
			t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
		}
		if run.Status != "pending" {
			t.Fatalf("run status = %q, want pending", run.Status)
		}

		replayResp := postWebhookRequest(t, handler, failDelivery, "Merge Request Hook", body)
		if replayResp.Code != http.StatusOK {
			t.Fatalf("expected 200 replay response, got %d: %s", replayResp.Code, replayResp.Body.String())
		}
		var duplicateResp map[string]string
		if err := json.NewDecoder(replayResp.Body).Decode(&duplicateResp); err != nil {
			t.Fatalf("decode replay response: %v", err)
		}
		if duplicateResp["status"] != "duplicate" {
			t.Fatalf("replay status = %q, want duplicate", duplicateResp["status"])
		}

		assertHookEventCount(t, reopenedDB, failDelivery, 1)
		assertReviewRunCountByKey(t, reopenedDB, normalizedRunKey("https://gitlab.example.com", 42, 7, "abc123def456", "webhook"), 1)
	})

	t.Run("command write rollback and retry recovery", func(t *testing.T) {
		sqlDB := setupTestDB(t)
		logger := testLogger()
		runSvc := runs.NewService(logger, sqlDB)
		handler := hooks.NewHandler(logger, sqlDB, "test-secret", runSvc)
		cmdProc := commands.NewProcessor(logger, sqlDB)
		handler.SetCommandProcessor(cmdProc)

		seedCommandEntities(t, sqlDB, "abc123def456")
		cmdDelivery := "service-db-fail-command"
		noteBody := []byte(`{
			"object_kind":"note",
			"event_type":"note",
			"user":{"username":"reviewer"},
			"project":{"id":42,"path_with_namespace":"test/repo","web_url":"https://gitlab.example.com/test/repo"},
			"object_attributes":{"note":"/ai-review rerun","noteable_type":"MergeRequest","discussion_id":"","url":"https://gitlab.example.com/test/repo/-/merge_requests/7#note_1"},
			"merge_request":{"iid":7,"title":"Add feature X","state":"opened","source_branch":"feature-x","target_branch":"main","last_commit":{"id":"abc123def456"},"url":"https://gitlab.example.com/test/repo/-/merge_requests/7"}
		}`)

		closeTestDBConnection(t, sqlDB)
		failResp := postWebhookRequest(t, handler, cmdDelivery, "Note Hook", noteBody)
		if failResp.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 during command DB outage, got %d: %s", failResp.Code, failResp.Body.String())
		}

		reopenedDB := setupTestDB(t)
		defer reopenedDB.Close()
		seedCommandEntities(t, reopenedDB, "abc123def456")
		queries := db.New(reopenedDB)
		assertNoHookEvent(t, queries, cmdDelivery)
		assertReviewRunCountByTrigger(t, reopenedDB, "command", 0)

		handler = hooks.NewHandler(logger, reopenedDB, "test-secret", runs.NewService(logger, reopenedDB))
		handler.SetCommandProcessor(commands.NewProcessor(logger, reopenedDB))

		successResp := postWebhookRequest(t, handler, cmdDelivery, "Note Hook", noteBody)
		if successResp.Code != http.StatusOK {
			t.Fatalf("expected 200 after command DB recovery, got %d: %s", successResp.Code, successResp.Body.String())
		}

		hookEvent, err := queries.GetHookEventByDeliveryKey(context.Background(), cmdDelivery)
		if err != nil {
			t.Fatalf("GetHookEventByDeliveryKey: %v", err)
		}
		if hookEvent.ID == 0 {
			t.Fatal("expected hook_event after command retry")
		}

		commandKey := commandRunKey("https://gitlab.example.com", 42, 7, "abc123def456", cmdDelivery)
		run, err := queries.GetReviewRunByIdempotencyKey(context.Background(), commandKey)
		if err != nil {
			t.Fatalf("GetReviewRunByIdempotencyKey(command): %v", err)
		}
		if run.TriggerType != "command" || run.Status != "pending" {
			t.Fatalf("command run = %+v, want trigger_type=command status=pending", run)
		}

		replayResp := postWebhookRequest(t, handler, cmdDelivery, "Note Hook", noteBody)
		if replayResp.Code != http.StatusOK {
			t.Fatalf("expected 200 replay response, got %d: %s", replayResp.Code, replayResp.Body.String())
		}
		var replay map[string]string
		if err := json.NewDecoder(replayResp.Body).Decode(&replay); err != nil {
			t.Fatalf("decode replay body: %v", err)
		}
		if replay["status"] != "duplicate" {
			t.Fatalf("replay status = %q, want duplicate", replay["status"])
		}

		assertHookEventCount(t, reopenedDB, cmdDelivery, 1)
		assertReviewRunCountByKey(t, reopenedDB, commandKey, 1)
	})

	t.Run("writer action rollback and retry recovery", func(t *testing.T) {
		sqlDB := setupTestDB(t)
		ctx := context.Background()
		seed := seedWriterEntities(t, sqlDB)
		writerStore := newSQLWriterStore(sqlDB)
		w := writer.New(fakeDiscussionClient{discussionID: "discussion-1", noteID: "note-1"}, writerStore)

		closeTestDBConnection(t, sqlDB)
		writeErr := w.Write(ctx, seed.run, []db.ReviewFinding{seed.finding})
		if writeErr == nil {
			t.Fatal("expected writer failure during DB outage")
		}

		reopenedDB := setupTestDB(t)
		defer reopenedDB.Close()
		seed = seedWriterEntities(t, reopenedDB)
		queries := db.New(reopenedDB)
		assertNoCommentAction(t, queries, "run:1:finding:1:create_discussion")
		assertCountQuery(t, reopenedDB, "SELECT COUNT(*) FROM gitlab_discussions", 0)

		w = writer.New(fakeDiscussionClient{discussionID: "discussion-1", noteID: "note-1"}, newSQLWriterStore(reopenedDB))
		if err := w.Write(ctx, seed.run, []db.ReviewFinding{seed.finding}); err != nil {
			t.Fatalf("writer retry after DB recovery: %v", err)
		}

		actions, err := queries.ListCommentActionsByRun(ctx, seed.run.ID)
		if err != nil {
			t.Fatalf("ListCommentActionsByRun: %v", err)
		}
		if len(actions) != 2 {
			t.Fatalf("comment action count = %d, want 2 (discussion + summary)", len(actions))
		}
		assertCountQuery(t, reopenedDB, "SELECT COUNT(*) FROM gitlab_discussions", 1)

		if err := w.Write(ctx, seed.run, []db.ReviewFinding{seed.finding}); err != nil {
			t.Fatalf("writer replay after recovery: %v", err)
		}
		actions, err = queries.ListCommentActionsByRun(ctx, seed.run.ID)
		if err != nil {
			t.Fatalf("ListCommentActionsByRun replay: %v", err)
		}
		if len(actions) != 2 {
			t.Fatalf("comment action count after replay = %d, want 2", len(actions))
		}
		assertCountQuery(t, reopenedDB, "SELECT COUNT(*) FROM gitlab_discussions", 1)
	})
}

type fakeDiscussionClient struct {
	discussionID string
	noteID       string
}

func (f fakeDiscussionClient) CreateDiscussion(_ context.Context, _ writer.CreateDiscussionRequest) (writer.Discussion, error) {
	return writer.Discussion{ID: f.discussionID}, nil
}

func (f fakeDiscussionClient) CreateNote(_ context.Context, _ writer.CreateNoteRequest) (writer.Discussion, error) {
	return writer.Discussion{ID: f.noteID}, nil
}

func (f fakeDiscussionClient) ResolveDiscussion(_ context.Context, _ writer.ResolveDiscussionRequest) error {
	return nil
}

type writerSeed struct {
	run     db.ReviewRun
	finding db.ReviewFinding
}

type sqlWriterStore struct {
	queries *db.Queries
}

func newSQLWriterStore(sqlDB *sql.DB) *sqlWriterStore {
	return &sqlWriterStore{queries: db.New(sqlDB)}
}

func (s *sqlWriterStore) GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error) {
	return s.queries.GetLatestMRVersion(ctx, mergeRequestID)
}

func (s *sqlWriterStore) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
	return s.queries.GetMergeRequest(ctx, id)
}

func (s *sqlWriterStore) GetReviewRun(ctx context.Context, id int64) (db.ReviewRun, error) {
	return s.queries.GetReviewRun(ctx, id)
}

func (s *sqlWriterStore) GetProjectPolicy(ctx context.Context, projectID int64) (db.ProjectPolicy, error) {
	return s.queries.GetProjectPolicy(ctx, projectID)
}

func (s *sqlWriterStore) GetReviewFinding(ctx context.Context, id int64) (db.ReviewFinding, error) {
	return s.queries.GetReviewFinding(ctx, id)
}

func (s *sqlWriterStore) GetGitlabDiscussion(ctx context.Context, id int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussion(ctx, id)
}

func (s *sqlWriterStore) ListFindingsByRun(ctx context.Context, reviewRunID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListFindingsByRun(ctx, reviewRunID)
}

func (s *sqlWriterStore) ListFindingsByMergeRequest(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListActiveFindingsByMR(ctx, mergeRequestID)
}

func (s *sqlWriterStore) GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error) {
	return s.queries.GetCommentActionByIdempotencyKey(ctx, idempotencyKey)
}

func (s *sqlWriterStore) GetGitlabDiscussionByFinding(ctx context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByFinding(ctx, reviewFindingID)
}

func (s *sqlWriterStore) GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByMergeRequestAndFinding(ctx, arg)
}

func (s *sqlWriterStore) InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	return s.queries.InsertCommentAction(ctx, arg)
}

func (s *sqlWriterStore) UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error {
	return s.queries.UpdateCommentActionStatus(ctx, arg)
}

func (s *sqlWriterStore) InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	return s.queries.InsertGitlabDiscussion(ctx, arg)
}

func (s *sqlWriterStore) UpdateFindingDiscussionID(ctx context.Context, arg db.UpdateFindingDiscussionIDParams) error {
	return s.queries.UpdateFindingDiscussionID(ctx, arg)
}

func (s *sqlWriterStore) UpdateGitlabDiscussionResolved(ctx context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error {
	return s.queries.UpdateGitlabDiscussionResolved(ctx, arg)
}

func (s *sqlWriterStore) UpdateGitlabDiscussionSupersededBy(ctx context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error {
	return s.queries.UpdateGitlabDiscussionSupersededBy(ctx, arg)
}

func (s *sqlWriterStore) MarkReviewRunFailedIfRunning(ctx context.Context, arg db.MarkReviewRunFailedParams) (bool, error) {
	return s.queries.MarkReviewRunFailedIfRunning(ctx, arg)
}

func seedWriterEntities(t *testing.T, sqlDB *sql.DB) writerSeed {
	t.Helper()
	instanceID := insertRow(t, sqlDB, "INSERT INTO gitlab_instances (url, name) VALUES ('https://gitlab.example.com', 'GitLab')")
	projectID := insertRow(t, sqlDB, "INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, 42, 'test/repo', TRUE)", instanceID)
	mrID := insertRow(t, sqlDB, `INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha, author, web_url)
		VALUES (?, 7, 'Add feature X', 'opened', 'main', 'feature-x', 'abc123def456', 'tester', 'https://gitlab.example.com/test/repo/-/merge_requests/7')`, projectID)
	versionID := insertRow(t, sqlDB, "INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha) VALUES (?, 1, 'base', 'start', 'abc123def456', 'patch')", mrID)
	_ = versionID
	runID := insertRow(t, sqlDB, "INSERT INTO review_runs (project_id, merge_request_id, status, trigger_type, idempotency_key, head_sha, max_retries) VALUES (?, ?, 'completed', 'webhook', 'writer-run-key', 'abc123def456', 3)", projectID, mrID)
	findingID := insertRow(t, sqlDB, `INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Database rollback safety', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-1', 'semantic-1', 'active')`, runID, mrID)

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	finding, err := db.New(sqlDB).GetReviewFinding(context.Background(), findingID)
	if err != nil {
		t.Fatalf("GetReviewFinding: %v", err)
	}
	return writerSeed{run: run, finding: finding}
}

func seedCommandEntities(t *testing.T, sqlDB *sql.DB, headSHA string) {
	t.Helper()
	instanceID := insertRow(t, sqlDB, "INSERT INTO gitlab_instances (url, name) VALUES ('https://gitlab.example.com', 'GitLab')")
	projectID := insertRow(t, sqlDB, "INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, 42, 'test/repo', TRUE)", instanceID)
	insertRow(t, sqlDB, `INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, is_draft, head_sha, web_url)
		VALUES (?, 7, 'Add feature X', 'feature-x', 'main', 'reviewer', 'opened', FALSE, ?, 'https://gitlab.example.com/test/repo/-/merge_requests/7')`, projectID, headSHA)
}

func insertRow(t *testing.T, sqlDB *sql.DB, query string, args ...any) int64 {
	t.Helper()
	res, err := sqlDB.Exec(query, args...)
	if err != nil {
		t.Fatalf("insert row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

func postWebhookRequest(t *testing.T, handler http.Handler, deliveryKey, eventType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-secret")
	req.Header.Set("X-Gitlab-Event", eventType)
	req.Header.Set("X-Gitlab-Delivery", deliveryKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func closeTestDBConnection(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func assertNoHookEvent(t *testing.T, queries *db.Queries, deliveryKey string) {
	t.Helper()
	_, err := queries.GetHookEventByDeliveryKey(context.Background(), deliveryKey)
	if err == nil {
		t.Fatalf("expected no hook_event for %q", deliveryKey)
	}
	if err != sql.ErrNoRows && !strings.Contains(err.Error(), "sql: no rows") {
		t.Fatalf("GetHookEventByDeliveryKey unexpected error: %v", err)
	}
}

func assertNoCommentAction(t *testing.T, queries *db.Queries, idempotencyKey string) {
	t.Helper()
	_, err := queries.GetCommentActionByIdempotencyKey(context.Background(), idempotencyKey)
	if err == nil {
		t.Fatalf("expected no comment action for %q", idempotencyKey)
	}
	if err != sql.ErrNoRows && !strings.Contains(err.Error(), "sql: no rows") {
		t.Fatalf("GetCommentActionByIdempotencyKey unexpected error: %v", err)
	}
}

func assertNoReviewRunByKey(t *testing.T, sqlDB *sql.DB, key string) {
	t.Helper()
	assertReviewRunCountByKey(t, sqlDB, key, 0)
}

func assertHookEventCount(t *testing.T, sqlDB *sql.DB, deliveryKey string, want int) {
	t.Helper()
	assertCountQueryArgs(t, sqlDB, "SELECT COUNT(*) FROM hook_events WHERE delivery_key = ?", want, deliveryKey)
}

func assertReviewRunCountByKey(t *testing.T, sqlDB *sql.DB, key string, want int) {
	t.Helper()
	assertCountQueryArgs(t, sqlDB, "SELECT COUNT(*) FROM review_runs WHERE idempotency_key = ?", want, key)
}

func assertReviewRunCountByTrigger(t *testing.T, sqlDB *sql.DB, triggerType string, want int) {
	t.Helper()
	assertCountQueryArgs(t, sqlDB, "SELECT COUNT(*) FROM review_runs WHERE trigger_type = ?", want, triggerType)
}

func assertCountQuery(t *testing.T, sqlDB *sql.DB, query string, want int) {
	t.Helper()
	assertCountQueryArgs(t, sqlDB, query, want)
}

func assertCountQueryArgs(t *testing.T, sqlDB *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := sqlDB.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}

func normalizedRunKey(baseURL string, projectID, mrIID int64, headSHA, triggerType string) string {
	payload := fmt.Sprintf("%s|%d|%d|%s|%s", strings.TrimRight(baseURL, "/"), projectID, mrIID, headSHA, triggerType)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:16])
}

func commandRunKey(baseURL string, projectID, mrIID int64, headSHA, deliveryKey string) string {
	payload := fmt.Sprintf("cmd|%s|%d|%d|%s|%s|%s", strings.TrimRight(baseURL, "/"), projectID, mrIID, headSHA, "command_rerun", deliveryKey)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("cmd-%x", sum[:16])
}

func intString(v int64) string {
	return strconv.FormatInt(v, 10)
}
