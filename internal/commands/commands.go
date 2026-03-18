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
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
)

// commandPrefix is the slash-command prefix we recognize.
const commandPrefix = "/ai-review"

// CommandKind identifies the type of /ai-review command.
type CommandKind string

const (
	CommandRerun   CommandKind = "rerun"
	CommandIgnore  CommandKind = "ignore"
	CommandResolve CommandKind = "resolve"
	CommandFocus   CommandKind = "focus"
	CommandUnknown CommandKind = "unknown"
)

// ParsedCommand represents a parsed /ai-review command from a note body.
type ParsedCommand struct {
	// Kind is the command type (rerun, ignore, resolve, focus, unknown).
	Kind CommandKind

	// Args contains any arguments after the command keyword.
	// For focus: the path pattern. For others: typically empty.
	Args string
}

// Parse extracts a /ai-review command from a note body. Returns nil if
// the note does not contain a /ai-review command.
func Parse(noteBody string) *ParsedCommand {
	// Trim whitespace and look for the command prefix.
	trimmed := strings.TrimSpace(noteBody)
	if !strings.HasPrefix(trimmed, commandPrefix) {
		return nil
	}

	// After the prefix, the next character must be whitespace or end of string
	// to avoid matching "/ai-review-something" as a command.
	afterPrefix := trimmed[len(commandPrefix):]
	if len(afterPrefix) > 0 && afterPrefix[0] != ' ' && afterPrefix[0] != '\t' && afterPrefix[0] != '\n' {
		return nil
	}

	// Extract the remainder after the prefix.
	rest := strings.TrimSpace(afterPrefix)

	// Split into command and args.
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		// Bare "/ai-review" with no subcommand — treat as unknown.
		return &ParsedCommand{Kind: CommandUnknown}
	}

	keyword := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	switch keyword {
	case "rerun":
		return &ParsedCommand{Kind: CommandRerun, Args: args}
	case "ignore":
		return &ParsedCommand{Kind: CommandIgnore, Args: args}
	case "resolve":
		return &ParsedCommand{Kind: CommandResolve, Args: args}
	case "focus":
		return &ParsedCommand{Kind: CommandFocus, Args: args}
	default:
		return &ParsedCommand{Kind: CommandUnknown, Args: rest}
	}
}

// IsCommand returns true if the note body starts with /ai-review.
func IsCommand(noteBody string) bool {
	return strings.HasPrefix(strings.TrimSpace(noteBody), commandPrefix)
}

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
// The cmd parameter can be a *ParsedCommand or an opaque value implementing
// Kind() string and Args() string methods (used when called from the hooks
// package to avoid circular imports).
func (p *Processor) Execute(ctx context.Context, noteEvent hooks.NormalizedNoteEvent, cmd interface{}) error {
	if cmd == nil {
		return nil
	}

	parsed := toParsedCommand(cmd)
	if parsed == nil {
		return nil
	}

	switch parsed.Kind {
	case CommandRerun:
		return p.executeRerun(ctx, noteEvent, parsed)
	case CommandIgnore:
		return p.executeIgnore(ctx, noteEvent)
	case CommandResolve:
		return p.executeResolve(ctx, noteEvent)
	case CommandFocus:
		return p.executeFocus(ctx, noteEvent, parsed)
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

// commandInterface is implemented by opaque command values from the hooks
// package to avoid circular dependency.
type commandInterface interface {
	Kind() string
	Args() string
}

// toParsedCommand converts either a *ParsedCommand or a commandInterface
// into a *ParsedCommand.
func toParsedCommand(cmd interface{}) *ParsedCommand {
	if pc, ok := cmd.(*ParsedCommand); ok {
		return pc
	}
	if ci, ok := cmd.(commandInterface); ok {
		kind := CommandUnknown
		switch ci.Kind() {
		case "rerun":
			kind = CommandRerun
		case "ignore":
			kind = CommandIgnore
		case "resolve":
			kind = CommandResolve
		case "focus":
			kind = CommandFocus
		default:
			kind = CommandUnknown
		}
		return &ParsedCommand{Kind: kind, Args: ci.Args()}
	}
	return nil
}

// executeRerun creates a new review run for the current HEAD SHA, bypassing
// the normal idempotency check by using a command-specific idempotency key.
func (p *Processor) executeRerun(ctx context.Context, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	return db.RunTx(ctx, p.db, func(ctx context.Context, q *db.Queries) error {
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

		// Generate a unique idempotency key for the command-triggered rerun.
		// This bypasses the normal webhook dedup so reruns always create new runs.
		idempotencyKey := computeCommandIdempotencyKey(
			noteEvent.GitLabInstanceURL, noteEvent.ProjectID, noteEvent.MRIID,
			headSHA, "command_rerun",
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
			if isDuplicateKeyError(err) {
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
	})
}

// executeIgnore marks the target finding as ignored and resolves the bot discussion.
func (p *Processor) executeIgnore(ctx context.Context, noteEvent hooks.NormalizedNoteEvent) error {
	if noteEvent.DiscussionID == "" {
		p.logger.WarnContext(ctx, "ignore command: no discussion context, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	return db.RunTx(ctx, p.db, func(ctx context.Context, q *db.Queries) error {
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
	})
}

// executeResolve resolves the bot discussion but leaves the finding active.
func (p *Processor) executeResolve(ctx context.Context, noteEvent hooks.NormalizedNoteEvent) error {
	if noteEvent.DiscussionID == "" {
		p.logger.WarnContext(ctx, "resolve command: no discussion context, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	return db.RunTx(ctx, p.db, func(ctx context.Context, q *db.Queries) error {
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
	})
}

// executeFocus creates a new run scoped to matching path patterns.
func (p *Processor) executeFocus(ctx context.Context, noteEvent hooks.NormalizedNoteEvent, cmd *ParsedCommand) error {
	if cmd.Args == "" {
		p.logger.WarnContext(ctx, "focus command: no path specified, skipping",
			"mr_iid", noteEvent.MRIID,
			"project_id", noteEvent.ProjectID,
		)
		return nil
	}

	return db.RunTx(ctx, p.db, func(ctx context.Context, q *db.Queries) error {
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
			headSHA, "command_focus_"+cmd.Args,
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
			if isDuplicateKeyError(err) {
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
	})
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

// computeCommandIdempotencyKey generates a unique key for command-triggered runs.
// It includes a timestamp nonce to ensure that repeated rerun commands always
// create new runs (the feature requirement says "even when a prior run exists").
func computeCommandIdempotencyKey(instanceURL string, projectID, mrIID int64, headSHA, commandContext string) string {
	input := fmt.Sprintf("cmd|%s|%d|%d|%s|%s|%d",
		strings.TrimRight(instanceURL, "/"),
		projectID,
		mrIID,
		headSHA,
		commandContext,
		time.Now().UnixNano(),
	)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("cmd-%x", hash[:16])
}

// isDuplicateKeyError checks if a MySQL error is a duplicate key violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Duplicate entry") ||
		strings.Contains(err.Error(), "Error 1062")
}
