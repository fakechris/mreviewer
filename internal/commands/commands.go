// Package commands implements the /ai-review command control plane.
// It parses note bodies for slash commands, routes them to the appropriate
// handler, and mutates finding/discussion/run state accordingly.
package commands

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/notecommand"
)

type CommandKind = notecommand.CommandKind
type ParsedCommand = notecommand.ParsedCommand

const (
	CommandRerun   = notecommand.CommandRerun
	CommandIgnore  = notecommand.CommandIgnore
	CommandResolve = notecommand.CommandResolve
	CommandFocus   = notecommand.CommandFocus
	CommandUnknown = notecommand.CommandUnknown
)

func Parse(noteBody string) *ParsedCommand { return notecommand.Parse(noteBody) }

func IsCommand(noteBody string) bool { return notecommand.IsCommand(noteBody) }

// defaultMaxRetries is the retry count for command-triggered runs.
const defaultMaxRetries = 3

// Processor executes /ai-review commands against the database.
type Processor struct {
	logger *slog.Logger
	db     *sql.DB
}

// NewProcessor creates a command processor.
func NewProcessor(logger *slog.Logger, database *sql.DB) *Processor {
	return &Processor{
		logger: logger,
		db:     database,
	}
}

// Execute processes a parsed command in the context of a note event.
// It returns nil for unknown commands (no side effects).
// Each command runs in its own database transaction.
// The cmd parameter can be a *ParsedCommand or an opaque value implementing
// Kind() string and Args() string methods (used when called from the hooks
// package to avoid circular imports).
func (p *Processor) Execute(ctx context.Context, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	return db.RunTx(ctx, p.db, func(ctx context.Context, q *db.Queries) error {
		return p.ExecuteWithQuerier(ctx, q, noteEvent, cmd)
	})
}

// ExecuteWithQuerier processes a parsed command using the provided querier,
// which may be scoped to an existing transaction. This allows callers (such as
// the webhook handler) to include hook-event persistence and command execution
// in a single atomic transaction, preventing partial failures where the
// hook_event is committed but the command effect is lost.
func (p *Processor) ExecuteWithQuerier(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	if cmd == nil {
		return nil
	}

	switch cmd.Kind {
	case CommandRerun:
		return p.executeRerunWith(ctx, q, noteEvent, cmd)
	case CommandIgnore:
		return p.executeIgnoreWith(ctx, q, noteEvent)
	case CommandResolve:
		return p.executeResolveWith(ctx, q, noteEvent)
	case CommandFocus:
		return p.executeFocusWith(ctx, q, noteEvent, cmd)
	case CommandUnknown:
		p.logger.InfoContext(ctx, "ignoring unknown /ai-review command",
			"note_body", noteEvent.NoteBody,
			"author", noteEvent.NoteAuthor,
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	default:
		return nil
	}
}

// executeRerunWith creates a new review run for the current HEAD SHA using the
// provided querier (which may be scoped to a caller-managed transaction).
func (p *Processor) executeRerunWith(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	// Resolve project and MR.
	instanceID, projectID, mrID, err := p.resolveEntities(ctx, q, noteEvent)
	if err != nil {
		return fmt.Errorf("rerun: resolve entities: %w", err)
	}
	_ = instanceID

	// Get the current HEAD SHA from the MR record.
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		return fmt.Errorf("rerun: get merge request: %w", err)
	}

	headSHA := noteEvent.HeadSHA
	if headSHA == "" {
		headSHA = mr.HeadSha
	}

	// Generate a stable idempotency key for the command-triggered rerun.
	// Derived from the delivery key so replayed deliveries are deduplicated,
	// but genuinely new rerun commands (with different delivery IDs) create new runs.
	idempotencyKey := computeCommandIdempotencyKey(
		noteEvent.GitLabInstanceURL, noteEvent.ProjectID, noteEvent.MRIID,
		headSHA, "command_rerun", noteEvent.DeliveryKey,
	)

	_, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "command",
		HeadSha:        headSHA,
		Status:         "pending",
		MaxRetries:     defaultMaxRetries,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if db.IsDuplicateKeyError(err) {
			p.logger.InfoContext(ctx, "rerun command: run already exists for this command",
				"idempotency_key", idempotencyKey,
				"mr_iid", noteEvent.MRIID,
			)
			return nil
		}
		return fmt.Errorf("rerun: insert review run: %w", err)
	}

	p.logger.InfoContext(ctx, "created rerun from command",
		"mr_iid", noteEvent.MRIID,
		"project_id", noteEvent.ProjectID,
		"head_sha", headSHA,
		"idempotency_key", idempotencyKey,
	)

	return nil
}

// executeIgnoreWith marks the target finding as ignored and resolves the bot discussion
// using the provided querier.
func (p *Processor) executeIgnoreWith(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent) error {
	if noteEvent.DiscussionID == "" {
		p.logger.WarnContext(ctx, "ignore command: no discussion context, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	_, _, mrID, err := p.resolveEntities(ctx, q, noteEvent)
	if err != nil {
		return fmt.Errorf("ignore: resolve entities: %w", err)
	}

	// Find the finding associated with this discussion.
	finding, err := q.GetFindingByMRAndDiscussionID(ctx, db.GetFindingByMRAndDiscussionIDParams{
		MergeRequestID:     mrID,
		GitlabDiscussionID: noteEvent.DiscussionID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			p.logger.WarnContext(ctx, "ignore command: no finding for discussion",
				"discussion_id", noteEvent.DiscussionID,
				"mr_iid", noteEvent.MRIID,
			)
			return nil
		}
		return fmt.Errorf("ignore: get finding by discussion: %w", err)
	}

	// Transition finding to ignored state.
	if err := q.UpdateFindingState(ctx, db.UpdateFindingStateParams{
		State:            "ignored",
		MatchedFindingID: sql.NullInt64{},
		ID:               finding.ID,
	}); err != nil {
		return fmt.Errorf("ignore: update finding state: %w", err)
	}

	// Resolve the bot discussion.
	disc, err := q.GetGitlabDiscussionByMergeRequestAndFinding(ctx, db.GetGitlabDiscussionByMergeRequestAndFindingParams{
		MergeRequestID:  mrID,
		ReviewFindingID: finding.ID,
	})
	if err == nil {
		if err := q.UpdateGitlabDiscussionResolved(ctx, db.UpdateGitlabDiscussionResolvedParams{
			Resolved: true,
			ID:       disc.ID,
		}); err != nil {
			return fmt.Errorf("ignore: resolve discussion: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("ignore: get discussion: %w", err)
	}

	p.logger.InfoContext(ctx, "finding marked ignored and discussion resolved",
		"finding_id", finding.ID,
		"discussion_id", noteEvent.DiscussionID,
		"mr_iid", noteEvent.MRIID,
	)

	return nil
}

// executeResolveWith resolves the bot discussion but leaves the finding active,
// using the provided querier.
func (p *Processor) executeResolveWith(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent) error {
	if noteEvent.DiscussionID == "" {
		p.logger.WarnContext(ctx, "resolve command: no discussion context, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	_, _, mrID, err := p.resolveEntities(ctx, q, noteEvent)
	if err != nil {
		return fmt.Errorf("resolve: resolve entities: %w", err)
	}

	// Find the finding associated with this discussion.
	finding, err := q.GetFindingByMRAndDiscussionID(ctx, db.GetFindingByMRAndDiscussionIDParams{
		MergeRequestID:     mrID,
		GitlabDiscussionID: noteEvent.DiscussionID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			p.logger.WarnContext(ctx, "resolve command: no finding for discussion",
				"discussion_id", noteEvent.DiscussionID,
				"mr_iid", noteEvent.MRIID,
			)
			return nil
		}
		return fmt.Errorf("resolve: get finding by discussion: %w", err)
	}

	// Resolve the bot discussion but do NOT change finding state.
	disc, err := q.GetGitlabDiscussionByMergeRequestAndFinding(ctx, db.GetGitlabDiscussionByMergeRequestAndFindingParams{
		MergeRequestID:  mrID,
		ReviewFindingID: finding.ID,
	})
	if err == nil {
		if err := q.UpdateGitlabDiscussionResolved(ctx, db.UpdateGitlabDiscussionResolvedParams{
			Resolved: true,
			ID:       disc.ID,
		}); err != nil {
			return fmt.Errorf("resolve: resolve discussion: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("resolve: get discussion: %w", err)
	}

	p.logger.InfoContext(ctx, "discussion resolved (finding remains active)",
		"finding_id", finding.ID,
		"discussion_id", noteEvent.DiscussionID,
		"mr_iid", noteEvent.MRIID,
	)

	return nil
}

// executeFocusWith creates a new run scoped to matching path patterns using
// the provided querier.
func (p *Processor) executeFocusWith(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	if cmd.Args == "" {
		p.logger.WarnContext(ctx, "focus command: no path specified, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	instanceID, projectID, mrID, err := p.resolveEntities(ctx, q, noteEvent)
	if err != nil {
		return fmt.Errorf("focus: resolve entities: %w", err)
	}
	_ = instanceID

	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		return fmt.Errorf("focus: get merge request: %w", err)
	}

	headSHA := noteEvent.HeadSHA
	if headSHA == "" {
		headSHA = mr.HeadSha
	}

	// Build scope_json with the focus path patterns.
	scopeJSON, err := json.Marshal(map[string]interface{}{
		"focus_paths": []string{cmd.Args},
	})
	if err != nil {
		return fmt.Errorf("focus: marshal scope: %w", err)
	}

	idempotencyKey := computeCommandIdempotencyKey(
		noteEvent.GitLabInstanceURL, noteEvent.ProjectID, noteEvent.MRIID,
		headSHA, "command_focus_"+cmd.Args, noteEvent.DeliveryKey,
	)

	_, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "command",
		HeadSha:        headSHA,
		Status:         "pending",
		MaxRetries:     defaultMaxRetries,
		IdempotencyKey: idempotencyKey,
		ScopeJson:      json.RawMessage(scopeJSON),
	})
	if err != nil {
		if db.IsDuplicateKeyError(err) {
			p.logger.InfoContext(ctx, "focus command: run already exists for this scope",
				"idempotency_key", idempotencyKey,
				"mr_iid", noteEvent.MRIID,
			)
			return nil
		}
		return fmt.Errorf("focus: insert review run: %w", err)
	}

	p.logger.InfoContext(ctx, "created focus rerun from command",
		"mr_iid", noteEvent.MRIID,
		"project_id", noteEvent.ProjectID,
		"head_sha", headSHA,
		"focus_path", cmd.Args,
		"idempotency_key", idempotencyKey,
	)

	return nil
}

// resolveEntities looks up the gitlab_instance, project, and merge_request
// records for the given note event. Returns (instanceID, projectID, mrID, err).
func (p *Processor) resolveEntities(ctx context.Context, q *db.Queries, noteEvent hooks.NormalizedNoteEvent) (int64, int64, int64, error) {
	instance, err := q.GetGitlabInstanceByURL(ctx, noteEvent.GitLabInstanceURL)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("get instance: %w", err)
	}

	project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  noteEvent.ProjectID,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("get project: %w", err)
	}

	mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     noteEvent.MRIID,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("get merge request: %w", err)
	}

	return instance.ID, project.ID, mr.ID, nil
}

// computeCommandIdempotencyKey generates a stable key for command-triggered runs.
// The key is derived from the note delivery identifier (webhook delivery ID) so
// that replayed deliveries of the same note event produce the same key, preventing
// duplicate runs. When a user sends a genuinely new /ai-review command, the
// delivery ID is different, so a new run is correctly created.
func computeCommandIdempotencyKey(instanceURL string, projectID, mrIID int64, headSHA, commandContext, deliveryKey string) string {
	input := fmt.Sprintf("cmd|%s|%d|%d|%s|%s|%s",
		strings.TrimRight(instanceURL, "/"),
		projectID,
		mrIID,
		headSHA,
		commandContext,
		deliveryKey,
	)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("cmd-%x", hash[:16])
}
