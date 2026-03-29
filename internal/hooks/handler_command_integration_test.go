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

	"github.com/mreviewer/mreviewer/internal/commands"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/notecommand"
	"github.com/mreviewer/mreviewer/internal/runs"
)

const commandTestWebhookKey = "CHANGEME" //nolint:gosec
const commandMigrationsDir = "../../migrations"

func commandTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupCommandTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, commandMigrationsDir)
	return sqlDB
}

func newCommandHandler(sqlDB *sql.DB) *hooks.Handler {
	logger := commandTestLogger()
	runProc := runs.NewService(logger, sqlDB)
	handler := hooks.NewHandler(logger, sqlDB, commandTestWebhookKey, runProc)
	cmdProc := commands.NewProcessor(logger, sqlDB)
	handler.SetCommandProcessor(cmdProc)
	return handler
}

func postCommandWebhook(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func commandHeaders(deliveryKey, eventType string) map[string]string {
	return map[string]string{
		"X-Gitlab-Token":    commandTestWebhookKey,
		"X-Gitlab-Event":    eventType,
		"X-Gitlab-Delivery": deliveryKey,
		"Content-Type":      "application/json",
	}
}

// notePayload generates a GitLab note webhook payload.
func notePayload(noteBody, noteableType, discussionID string) string {
	discField := ""
	if discussionID != "" {
		discField = fmt.Sprintf(`,"discussion_id": %q`, discussionID)
	}
	return fmt.Sprintf(`{
		"object_kind": "note",
		"event_type": "note",
		"user": {"username": "reviewer"},
		"project": {
			"id": 42,
			"path_with_namespace": "test/repo",
			"web_url": "https://gitlab.example.com/test/repo"
		},
		"object_attributes": {
			"note": %s,
			"noteable_type": %q%s
		},
		"merge_request": {
			"iid": 7,
			"last_commit": {"id": "abc123def456"}
		}
	}`, mustJSON(noteBody), noteableType, discField)
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// seedMRForCommandTest creates the prerequisite instance + project + MR + prior run
// for command handler integration tests.
func seedMRForCommandTest(t *testing.T, sqlDB *sql.DB) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	handler := newCommandHandler(sqlDB)

	// Post a standard MR open event to create the project, MR, and initial run.
	openPayload := `{
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

	rec := postCommandWebhook(handler, openPayload, commandHeaders("cmd-seed-open-1", "Merge Request Hook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed MR open: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	queries := db.New(sqlDB)
	instance, err := queries.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	project, err := queries.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  42,
	})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     7,
	})
	if err != nil {
		t.Fatalf("get MR: %v", err)
	}

	return project.ID, mr.ID
}

// TestNoteWebhookRouting verifies VAL-BETA-011:
// Note/comment webhook events whose body starts with /ai-review are routed
// to command processing.
func TestNoteWebhookRouting(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	seedMRForCommandTest(t, sqlDB)

	t.Run("note with command is processed", func(t *testing.T) {
		rec := postCommandWebhook(
			handler,
			notePayload("/ai-review rerun", "MergeRequest", ""),
			commandHeaders("cmd-note-route-1", "Note Hook"),
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp["command"] != "processed" {
			t.Errorf("expected command='processed', got %q", resp["command"])
		}
	})

	t.Run("note without command is accepted but not processed", func(t *testing.T) {
		rec := postCommandWebhook(
			handler,
			notePayload("This is a normal comment", "MergeRequest", ""),
			commandHeaders("cmd-note-normal-1", "Note Hook"),
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp["command"] != "none" {
			t.Errorf("expected command='none', got %q", resp["command"])
		}
	})

	t.Run("note on issue is ignored (not MR note)", func(t *testing.T) {
		rec := postCommandWebhook(
			handler,
			notePayload("/ai-review rerun", "Issue", ""),
			commandHeaders("cmd-note-issue-1", "Note Hook"),
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		// Non-MR notes should be treated as unsupported events (not routed to commands).
		if resp["status"] != "ignored" {
			t.Errorf("expected status='ignored' for non-MR note, got %q", resp["status"])
		}
	})
}

// TestRerunCommandViaHandler verifies VAL-BETA-006 end-to-end through the handler:
// Posting a note webhook with /ai-review rerun creates a new review run.
func TestRerunCommandViaHandler(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	_, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders("cmd-rerun-handler-1", "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Should have original open run + command rerun.
	allRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(allRuns) < 2 {
		t.Fatalf("expected at least 2 runs (open + rerun), got %d", len(allRuns))
	}

	var commandRun *db.ReviewRun
	for i := range allRuns {
		if allRuns[i].TriggerType == "command" {
			commandRun = &allRuns[i]
			break
		}
	}
	if commandRun == nil {
		t.Fatal("no command-triggered run found")
	}
	if commandRun.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", commandRun.Status)
	}
}

// TestIgnoreCommandViaHandler verifies VAL-BETA-007 end-to-end through the handler.
func TestIgnoreCommandViaHandler(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	projectID, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	findingID, discDBID := seedCommandFindingWithDiscussion(t, sqlDB, projectID, mrID, "disc-handler-ignore-1")

	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review ignore", "MergeRequest", "disc-handler-ignore-1"),
		commandHeaders("cmd-ignore-handler-1", "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify finding is ignored.
	finding, err := queries.GetReviewFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("get finding: %v", err)
	}
	if finding.State != "ignored" {
		t.Errorf("expected finding state 'ignored', got %q", finding.State)
	}

	// Verify discussion is resolved.
	disc, err := queries.GetGitlabDiscussion(ctx, discDBID)
	if err != nil {
		t.Fatalf("get discussion: %v", err)
	}
	if !disc.Resolved {
		t.Error("expected discussion to be resolved")
	}
}

// TestResolveCommandViaHandler verifies VAL-BETA-008 end-to-end through the handler.
func TestResolveCommandViaHandler(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	projectID, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	findingID, discDBID := seedCommandFindingWithDiscussion(t, sqlDB, projectID, mrID, "disc-handler-resolve-1")

	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review resolve", "MergeRequest", "disc-handler-resolve-1"),
		commandHeaders("cmd-resolve-handler-1", "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify finding state is still active.
	finding, err := queries.GetReviewFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("get finding: %v", err)
	}
	if finding.State != "active" {
		t.Errorf("expected finding state 'active', got %q", finding.State)
	}

	// Verify discussion IS resolved.
	disc, err := queries.GetGitlabDiscussion(ctx, discDBID)
	if err != nil {
		t.Fatalf("get discussion: %v", err)
	}
	if !disc.Resolved {
		t.Error("expected discussion to be resolved")
	}
}

// TestFocusCommandViaHandler verifies VAL-BETA-009 end-to-end through the handler.
func TestFocusCommandViaHandler(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	_, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review focus src/auth/", "MergeRequest", ""),
		commandHeaders("cmd-focus-handler-1", "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Should have a focus-scoped run.
	allRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	var focusRun *db.ReviewRun
	for i := range allRuns {
		if allRuns[i].TriggerType == "command" && allRuns[i].ScopeJson != nil {
			focusRun = &allRuns[i]
			break
		}
	}
	if focusRun == nil {
		t.Fatal("no focus-scoped command run found")
	}

	var scope map[string]interface{}
	if err := json.Unmarshal(focusRun.ScopeJson, &scope); err != nil {
		t.Fatalf("unmarshal scope_json: %v", err)
	}
	paths, ok := scope["focus_paths"].([]interface{})
	if !ok || len(paths) != 1 || paths[0] != "src/auth/" {
		t.Errorf("expected focus_paths=['src/auth/'], got %v", scope["focus_paths"])
	}
}

// TestUnknownCommandViaHandler verifies VAL-BETA-010 end-to-end through the handler:
// An unknown /ai-review command has no side effects.
func TestUnknownCommandViaHandler(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	_, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review foobar", "MergeRequest", ""),
		commandHeaders("cmd-unknown-handler-1", "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Should only have the initial open run, no command runs.
	allRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	for _, run := range allRuns {
		if run.TriggerType == "command" {
			t.Errorf("unexpected command-triggered run found (id=%d)", run.ID)
		}
	}
}

// testDlv builds a test delivery identifier from a prefix and suffix.
func testDlv(prefix, suffix string) string { return prefix + "-" + suffix }

// TestCommandIdempotencyStableDeliveryKey verifies that replaying the same
// note webhook delivery (same X-Gitlab-Delivery) for a rerun command does NOT
// create a duplicate review run. The first delivery creates the run; the replay
// is deduplicated at the hook_events level because the hook_event and command
// are persisted atomically.
func TestCommandIdempotencyStableDeliveryKey(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	handler := newCommandHandler(sqlDB)
	_, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	deliveryKey := testDlv("idem", "rerun1")

	// First delivery: creates hook_event + command run atomically.
	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("first delivery: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Replay: same delivery key should be deduplicated at the top-level
	// delivery-key check (the hook_event was committed with the command).
	rec = postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay delivery: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var replayResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&replayResp); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replayResp["status"] != "duplicate" {
		t.Errorf("expected status='duplicate' for replay, got %q", replayResp["status"])
	}

	// Verify exactly 1 command run was created (not 2).
	allRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	commandRuns := 0
	for _, r := range allRuns {
		if r.TriggerType == "command" {
			commandRuns++
		}
	}
	if commandRuns != 1 {
		t.Errorf("expected exactly 1 command run after replay, got %d", commandRuns)
	}

	// Verify exactly 1 hook_event for this delivery key.
	hookEvent, err := queries.GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("get hook event: %v", err)
	}
	if hookEvent.ID == 0 {
		t.Fatal("expected hook_event to exist")
	}
}

// TestCommandRetryAfterPersistenceFailure verifies that if the command
// execution fails (simulated by using a failing processor), the hook_event is
// NOT committed, allowing the retry to succeed. This tests the atomicity of
// the transactional flow.
func TestCommandRetryAfterPersistenceFailure(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	logger := commandTestLogger()
	runProc := runs.NewService(logger, sqlDB)
	handler := hooks.NewHandler(logger, sqlDB, commandTestWebhookKey, runProc)

	// Set up a failing command processor that fails on first call, succeeds on second.
	failProc := &failOnceCommandProcessor{
		real:      commands.NewProcessor(logger, sqlDB),
		failCount: 1,
	}
	handler.SetCommandProcessor(failProc)

	seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	deliveryKey := testDlv("retry", "fail1")

	// First attempt: command processor fails → hook_event should be rolled back.
	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt: expected 500 (command failure), got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify no hook_event was committed for this delivery key.
	_, err := queries.GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err == nil {
		t.Fatal("expected no hook_event after failed attempt, but found one (not rolled back)")
	}

	// Retry: should succeed now (failOnceCommandProcessor passes on second call).
	rec = postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if resp["command"] != "processed" {
		t.Errorf("expected command='processed' on retry, got %q", resp["command"])
	}

	// Verify the hook_event now exists.
	hookEvent, err := queries.GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("get hook event after retry: %v", err)
	}
	if hookEvent.ID == 0 {
		t.Fatal("expected hook_event to exist after successful retry")
	}
}

// TestCommandPersistenceAndExecutionAtomicity verifies that hook_event
// persistence and command execution are atomic: either both succeed or both
// are rolled back. This prevents the scenario where a hook_event is committed
// but the command effect is lost, causing the retry to be deduplicated
// (hook_event exists) while the command effect never took place.
func TestCommandPersistenceAndExecutionAtomicity(t *testing.T) {
	sqlDB := setupCommandTestDB(t)
	logger := commandTestLogger()
	runProc := runs.NewService(logger, sqlDB)
	handler := hooks.NewHandler(logger, sqlDB, commandTestWebhookKey, runProc)

	// Use a processor that always fails.
	alwaysFailProc := &alwaysFailCommandProcessor{}
	handler.SetCommandProcessor(alwaysFailProc)

	_, mrID := seedMRForCommandTest(t, sqlDB)
	ctx := context.Background()
	queries := db.New(sqlDB)

	deliveryKey := testDlv("atomic", "test1")

	// Attempt: command always fails → everything should be rolled back.
	rec := postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (command failure), got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify NO hook_event was committed.
	_, err := queries.GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err == nil {
		t.Fatal("atomicity violation: hook_event was committed despite command failure")
	}

	// Verify NO command runs were created for this delivery.
	allRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	for _, r := range allRuns {
		if r.TriggerType == "command" {
			t.Errorf("atomicity violation: command run was created despite failure (id=%d)", r.ID)
		}
	}

	// Now switch to a working processor and retry — the retry should succeed
	// because the hook_event was never committed.
	realProc := commands.NewProcessor(logger, sqlDB)
	handler.SetCommandProcessor(realProc)

	rec = postCommandWebhook(
		handler,
		notePayload("/ai-review rerun", "MergeRequest", ""),
		commandHeaders(deliveryKey, "Note Hook"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry after atomicity failure: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Now verify the hook_event and command run both exist.
	hookEvent, err := queries.GetHookEventByDeliveryKey(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("get hook event after successful retry: %v", err)
	}
	if hookEvent.ID == 0 {
		t.Fatal("expected hook_event to exist after successful retry")
	}

	allRuns, err = queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs after retry: %v", err)
	}
	commandRunCount := 0
	for _, r := range allRuns {
		if r.TriggerType == "command" {
			commandRunCount++
		}
	}
	if commandRunCount != 1 {
		t.Errorf("expected exactly 1 command run after retry, got %d", commandRunCount)
	}
}

// failOnceCommandProcessor fails the first N calls then delegates to the real processor.
type failOnceCommandProcessor struct {
	real      *commands.Processor
	failCount int
	calls     int
}

func (f *failOnceCommandProcessor) Execute(ctx context.Context, noteEvent hooks.NormalizedNoteEvent, cmd *notecommand.ParsedCommand) error {
	f.calls++
	if f.calls <= f.failCount {
		return fmt.Errorf("simulated command failure (call %d)", f.calls)
	}
	return f.real.Execute(ctx, noteEvent, cmd)
}

func (f *failOnceCommandProcessor) ExecuteWithQuerier(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent, cmd *notecommand.ParsedCommand) error {
	f.calls++
	if f.calls <= f.failCount {
		return fmt.Errorf("simulated command failure (call %d)", f.calls)
	}
	return f.real.ExecuteWithQuerier(ctx, q, noteEvent, cmd)
}

func (f *failOnceCommandProcessor) ExecuteWithStore(ctx context.Context, s db.Store, noteEvent hooks.NormalizedNoteEvent, cmd *notecommand.ParsedCommand) error {
	f.calls++
	if f.calls <= f.failCount {
		return fmt.Errorf("simulated command failure (call %d)", f.calls)
	}
	return f.real.ExecuteWithStore(ctx, s, noteEvent, cmd)
}

// alwaysFailCommandProcessor always returns an error.
type alwaysFailCommandProcessor struct{}

func (a *alwaysFailCommandProcessor) Execute(_ context.Context, _ hooks.NormalizedNoteEvent, _ *notecommand.ParsedCommand) error {
	return fmt.Errorf("simulated permanent command failure")
}

func (a *alwaysFailCommandProcessor) ExecuteWithQuerier(_ context.Context, _ *db.Queries, _ hooks.NormalizedNoteEvent, _ *notecommand.ParsedCommand) error {
	return fmt.Errorf("simulated permanent command failure")
}

func (a *alwaysFailCommandProcessor) ExecuteWithStore(_ context.Context, _ db.Store, _ hooks.NormalizedNoteEvent, _ *notecommand.ParsedCommand) error {
	return fmt.Errorf("simulated permanent command failure")
}

// seedCommandFindingWithDiscussion creates a finding and discussion for
// handler-level command integration tests.
func seedCommandFindingWithDiscussion(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, gitlabDiscID string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	// Use an existing run from the seed or create one.
	existingRuns, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil || len(existingRuns) == 0 {
		t.Fatalf("no seed run available for finding: %v", err)
	}
	runID := existingRuns[0].ID

	// Create the finding.
	findingResult, err := queries.InsertReviewFinding(ctx, db.InsertReviewFindingParams{
		ReviewRunID:         runID,
		MergeRequestID:      mrID,
		Category:            "security",
		Severity:            "high",
		Confidence:          0.9,
		Title:               "Potential SQL injection",
		Path:                "src/db.go",
		AnchorKind:          "new_line",
		NewLine:             sql.NullInt32{Int32: 42, Valid: true},
		CanonicalKey:        "sql-injection-" + gitlabDiscID,
		AnchorFingerprint:   "fp-anchor-" + gitlabDiscID,
		SemanticFingerprint: "fp-semantic-" + gitlabDiscID,
		State:               "active",
	})
	if err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	findingID, _ := findingResult.LastInsertId()

	// Set the discussion ID on the finding.
	if err := queries.UpdateFindingDiscussionID(ctx, db.UpdateFindingDiscussionIDParams{
		GitlabDiscussionID: gitlabDiscID,
		ID:                 findingID,
	}); err != nil {
		t.Fatalf("update finding discussion ID: %v", err)
	}

	// Create the gitlab_discussions row.
	discResult, err := queries.InsertGitlabDiscussion(ctx, db.InsertGitlabDiscussionParams{
		ReviewFindingID:    findingID,
		MergeRequestID:     mrID,
		GitlabDiscussionID: gitlabDiscID,
		DiscussionType:     "diff",
		Resolved:           false,
	})
	if err != nil {
		t.Fatalf("insert gitlab discussion: %v", err)
	}
	discID, _ := discResult.LastInsertId()

	return findingID, discID
}
