package commands_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/commands"
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

// seedProjectAndMR creates the prerequisite gitlab_instance, project, and
// merge_request rows needed for command tests. Returns (instanceID, projectID, mrID).
func seedProjectAndMR(t *testing.T, sqlDB *sql.DB, headSHA string) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	result, err := queries.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{
		Url:  "https://gitlab.example.com",
		Name: "GitLab",
	})
	if err != nil {
		t.Fatalf("upsert instance: %v", err)
	}
	instanceID, _ := result.LastInsertId()
	if instanceID == 0 {
		inst, err := queries.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}
		instanceID = inst.ID
	}

	result, err = queries.UpsertProject(ctx, db.UpsertProjectParams{
		GitlabInstanceID:  instanceID,
		GitlabProjectID:   42,
		PathWithNamespace: "test/repo",
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	projectID, _ := result.LastInsertId()
	if projectID == 0 {
		proj, err := queries.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
			GitlabInstanceID: instanceID,
			GitlabProjectID:  42,
		})
		if err != nil {
			t.Fatalf("get project: %v", err)
		}
		projectID = proj.ID
	}

	result, err = queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        7,
		Title:        "Add feature X",
		SourceBranch: "feature-x",
		TargetBranch: "main",
		Author:       "testuser",
		State:        "opened",
		IsDraft:      false,
		HeadSha:      headSHA,
		WebUrl:       "https://gitlab.example.com/test/repo/-/merge_requests/7",
	})
	if err != nil {
		t.Fatalf("upsert MR: %v", err)
	}
	mrID, _ := result.LastInsertId()
	if mrID == 0 {
		mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
			ProjectID: projectID,
			MrIid:     7,
		})
		if err != nil {
			t.Fatalf("get MR: %v", err)
		}
		mrID = mr.ID
	}

	return instanceID, projectID, mrID
}

// seedFindingWithDiscussion creates a review finding and its associated
// gitlab_discussion for the given MR. Returns (findingID, discussionID).
func seedFindingWithDiscussion(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, gitlabDiscID string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	// Create a review run for the finding.
	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "webhook",
		HeadSha:        "head-sha-abc123",
		Status:         "completed",
		MaxRetries:     3,
		IdempotencyKey: "test-run-" + gitlabDiscID,
	})
	if err != nil {
		t.Fatalf("insert review run: %v", err)
	}
	runID, _ := runResult.LastInsertId()

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
		CanonicalKey:        "sql-injection-1",
		AnchorFingerprint:   "fp-anchor-" + gitlabDiscID,
		SemanticFingerprint: "fp-semantic-" + gitlabDiscID,
		State:               "active",
	})
	if err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	findingID, _ := findingResult.LastInsertId()

	// Set the gitlab_discussion_id on the finding.
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

func baseNoteEvent(noteBody, discussionID string) hooks.NormalizedNoteEvent {
	return hooks.NormalizedNoteEvent{
		GitLabInstanceURL: "https://gitlab.example.com",
		ProjectID:         42,
		ProjectPath:       "test/repo",
		MRIID:             7,
		HeadSHA:           "head-sha-abc123",
		NoteBody:          noteBody,
		NoteAuthor:        "reviewer",
		DiscussionID:      discussionID,
		NoteableType:      "MergeRequest",
		HookSource:        "project",
	}
}

// TestRerunCommand verifies VAL-BETA-006:
// /ai-review rerun creates a new run for the current HEAD even when a prior run exists.
func TestRerunCommand(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, projectID, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	// Create a prior run.
	_, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "webhook",
		HeadSha:        "head-sha-abc123",
		Status:         "completed",
		MaxRetries:     3,
		IdempotencyKey: "test-prior-run-key",
	})
	if err != nil {
		t.Fatalf("insert prior run: %v", err)
	}

	// Execute rerun command.
	noteEvent := baseNoteEvent("/ai-review rerun", "")
	cmd := commands.Parse(noteEvent.NoteBody)
	if cmd == nil {
		t.Fatal("expected parsed command, got nil")
	}
	if cmd.Kind != commands.CommandRerun {
		t.Fatalf("expected rerun command, got %q", cmd.Kind)
	}

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("execute rerun: %v", err)
	}

	// Verify a new run was created.
	runs, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) < 2 {
		t.Fatalf("expected at least 2 runs (prior + rerun), got %d", len(runs))
	}

	// Find the command-triggered run.
	var commandRun *db.ReviewRun
	for i := range runs {
		if runs[i].TriggerType == "command" {
			commandRun = &runs[i]
			break
		}
	}
	if commandRun == nil {
		t.Fatal("no command-triggered run found")
	}
	if commandRun.Status != "pending" {
		t.Errorf("expected pending status, got %q", commandRun.Status)
	}
	if commandRun.HeadSha != "head-sha-abc123" {
		t.Errorf("expected head_sha 'head-sha-abc123', got %q", commandRun.HeadSha)
	}
}

// TestIgnoreCommand verifies VAL-BETA-007:
// /ai-review ignore marks the finding as ignored and resolves the discussion.
func TestIgnoreCommand(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, projectID, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	findingID, discDBID := seedFindingWithDiscussion(t, sqlDB, projectID, mrID, "disc-ignore-001")

	noteEvent := baseNoteEvent("/ai-review ignore", "disc-ignore-001")
	cmd := commands.Parse(noteEvent.NoteBody)
	if cmd == nil || cmd.Kind != commands.CommandIgnore {
		t.Fatal("expected ignore command")
	}

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("execute ignore: %v", err)
	}

	// Verify finding state is "ignored".
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

// TestResolveCommand verifies VAL-BETA-008:
// /ai-review resolve resolves the bot discussion while leaving the finding active.
func TestResolveCommand(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, projectID, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	findingID, discDBID := seedFindingWithDiscussion(t, sqlDB, projectID, mrID, "disc-resolve-001")

	noteEvent := baseNoteEvent("/ai-review resolve", "disc-resolve-001")
	cmd := commands.Parse(noteEvent.NoteBody)
	if cmd == nil || cmd.Kind != commands.CommandResolve {
		t.Fatal("expected resolve command")
	}

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("execute resolve: %v", err)
	}

	// Verify finding state remains "active" (not changed).
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

// TestFocusCommand verifies VAL-BETA-009:
// /ai-review focus <path> creates a rerun scoped to matching paths.
func TestFocusCommand(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, _, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	noteEvent := baseNoteEvent("/ai-review focus src/auth/", "")
	cmd := commands.Parse(noteEvent.NoteBody)
	if cmd == nil || cmd.Kind != commands.CommandFocus {
		t.Fatal("expected focus command")
	}
	if cmd.Args != "src/auth/" {
		t.Fatalf("expected args 'src/auth/', got %q", cmd.Args)
	}

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("execute focus: %v", err)
	}

	// Verify a new run was created with scope_json.
	runs, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) < 1 {
		t.Fatal("expected at least 1 run")
	}

	var focusRun *db.ReviewRun
	for i := range runs {
		if runs[i].TriggerType == "command" && runs[i].ScopeJson != nil {
			focusRun = &runs[i]
			break
		}
	}
	if focusRun == nil {
		t.Fatal("no focus-scoped command run found")
	}
	if focusRun.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", focusRun.Status)
	}
	if focusRun.HeadSha != "head-sha-abc123" {
		t.Errorf("expected head_sha 'head-sha-abc123', got %q", focusRun.HeadSha)
	}

	// Verify scope_json contains the focus path.
	var scope map[string]interface{}
	if err := json.Unmarshal(focusRun.ScopeJson, &scope); err != nil {
		t.Fatalf("unmarshal scope_json: %v", err)
	}
	paths, ok := scope["focus_paths"]
	if !ok {
		t.Fatal("scope_json missing 'focus_paths' key")
	}
	pathList, ok := paths.([]interface{})
	if !ok || len(pathList) != 1 {
		t.Fatalf("expected 1 focus path, got %v", paths)
	}
	if pathList[0] != "src/auth/" {
		t.Errorf("expected focus path 'src/auth/', got %v", pathList[0])
	}
}

// TestUnknownCommandIgnored verifies VAL-BETA-010:
// An unknown /ai-review command has no side effects.
func TestUnknownCommandIgnored(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, _, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	noteEvent := baseNoteEvent("/ai-review foobar", "")
	cmd := commands.Parse(noteEvent.NoteBody)
	if cmd == nil {
		t.Fatal("expected parsed command (unknown), got nil")
	}
	if cmd.Kind != commands.CommandUnknown {
		t.Fatalf("expected unknown command, got %q", cmd.Kind)
	}

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("execute unknown command: %v", err)
	}

	// Verify no runs were created.
	runs, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs for unknown command, got %d", len(runs))
	}
}

// TestIgnoreCommandNoDiscussion verifies that /ai-review ignore without
// a discussion context is a no-op (no error, no state change).
func TestIgnoreCommandNoDiscussion(t *testing.T) {
	sqlDB := setupTestDB(t)
	seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)

	noteEvent := baseNoteEvent("/ai-review ignore", "") // No discussion_id
	cmd := commands.Parse(noteEvent.NoteBody)

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("expected no error for ignore without discussion, got: %v", err)
	}
}

// TestResolveCommandNoDiscussion verifies that /ai-review resolve without
// a discussion context is a no-op.
func TestResolveCommandNoDiscussion(t *testing.T) {
	sqlDB := setupTestDB(t)
	seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)

	noteEvent := baseNoteEvent("/ai-review resolve", "")
	cmd := commands.Parse(noteEvent.NoteBody)

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("expected no error for resolve without discussion, got: %v", err)
	}
}

// TestFocusCommandNoPath verifies that /ai-review focus without a path is a no-op.
func TestFocusCommandNoPath(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, _, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	noteEvent := baseNoteEvent("/ai-review focus", "")
	cmd := commands.Parse(noteEvent.NoteBody)

	if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
		t.Fatalf("expected no error for focus without path, got: %v", err)
	}

	// Verify no runs created.
	runs, err := queries.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs for empty focus, got %d", len(runs))
	}
}

// TestCommandIdempotencyStableDeliveryKey verifies that replaying the same
// delivery identifier for a rerun or focus command produces the same
// idempotency key and therefore does NOT create a duplicate review run, while
// a genuinely new delivery identifier DOES create a new run.
// testDispatchID builds a harmless test dispatch identifier from a prefix and suffix.
func testDispatchID(prefix, suffix string) string { return prefix + "-" + suffix }

// noteEventWithDispatchID creates a NormalizedNoteEvent with a test dispatch identifier set.
func noteEventWithDispatchID(noteBody, discussionID, dispatchID string) hooks.NormalizedNoteEvent {
	ev := baseNoteEvent(noteBody, discussionID)
	ev.DeliveryKey = dispatchID //nolint:gosec // test fixture dispatch id, not a secret
	return ev
}

func TestCommandIdempotencyStableDeliveryKey(t *testing.T) {
	sqlDB := setupTestDB(t)
	_, _, mrID := seedProjectAndMR(t, sqlDB, "head-sha-abc123")
	ctx := context.Background()
	processor := commands.NewProcessor(testLogger(), sqlDB)
	queries := db.New(sqlDB)

	t.Run("rerun: same delivery key deduplicates", func(t *testing.T) {
		noteEvent := noteEventWithDispatchID("/ai-review rerun", "", testDispatchID("rerun", "1"))
		cmd := commands.Parse(noteEvent.NoteBody)

		// First execution: should create a run.
		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("first rerun execute: %v", err)
		}

		// Replay: same delivery key should be deduplicated (no new run).
		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("replay rerun execute: %v", err)
		}

		runs, err := queries.ListReviewRunsByMR(ctx, mrID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		commandRuns := 0
		for _, r := range runs {
			if r.TriggerType == "command" {
				commandRuns++
			}
		}
		if commandRuns != 1 {
			t.Errorf("expected exactly 1 command run after replay, got %d", commandRuns)
		}
	})

	t.Run("rerun: different delivery key creates new run", func(t *testing.T) {
		noteEvent := noteEventWithDispatchID("/ai-review rerun", "", testDispatchID("rerun", "2"))
		cmd := commands.Parse(noteEvent.NoteBody)

		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("second rerun execute: %v", err)
		}

		runs, err := queries.ListReviewRunsByMR(ctx, mrID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		commandRuns := 0
		for _, r := range runs {
			if r.TriggerType == "command" {
				commandRuns++
			}
		}
		if commandRuns != 2 {
			t.Errorf("expected 2 command runs after different delivery, got %d", commandRuns)
		}
	})

	t.Run("focus: same delivery key deduplicates", func(t *testing.T) {
		noteEvent := noteEventWithDispatchID("/ai-review focus src/models/", "", testDispatchID("focus", "1"))
		cmd := commands.Parse(noteEvent.NoteBody)

		// First execution.
		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("first focus execute: %v", err)
		}

		// Replay with same delivery key.
		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("replay focus execute: %v", err)
		}

		runs, err := queries.ListReviewRunsByMR(ctx, mrID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		focusRuns := 0
		for _, r := range runs {
			if r.TriggerType == "command" && r.ScopeJson != nil {
				var scope map[string]interface{}
				if json.Unmarshal(r.ScopeJson, &scope) == nil {
					if paths, ok := scope["focus_paths"]; ok {
						if pList, ok := paths.([]interface{}); ok && len(pList) > 0 && pList[0] == "src/models/" {
							focusRuns++
						}
					}
				}
			}
		}
		if focusRuns != 1 {
			t.Errorf("expected exactly 1 focus run after replay, got %d", focusRuns)
		}
	})

	t.Run("focus: different delivery key creates new run", func(t *testing.T) {
		noteEvent := noteEventWithDispatchID("/ai-review focus src/models/", "", testDispatchID("focus", "2"))
		cmd := commands.Parse(noteEvent.NoteBody)

		if err := processor.Execute(ctx, noteEvent, cmd); err != nil {
			t.Fatalf("second focus execute: %v", err)
		}

		runs, err := queries.ListReviewRunsByMR(ctx, mrID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		focusRuns := 0
		for _, r := range runs {
			if r.TriggerType == "command" && r.ScopeJson != nil {
				var scope map[string]interface{}
				if json.Unmarshal(r.ScopeJson, &scope) == nil {
					if paths, ok := scope["focus_paths"]; ok {
						if pList, ok := paths.([]interface{}); ok && len(pList) > 0 && pList[0] == "src/models/" {
							focusRuns++
						}
					}
				}
			}
		}
		if focusRuns != 2 {
			t.Errorf("expected 2 focus runs after different delivery, got %d", focusRuns)
		}
	})
}
