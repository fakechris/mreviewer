package gate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
)

type StatusPublisher interface {
	PublishStatus(ctx context.Context, result Result) error
}

type CIGatePublisher interface {
	PublishCIGate(ctx context.Context, result Result) error
}

type NoopStatusPublisher struct{}

func (NoopStatusPublisher) PublishStatus(context.Context, Result) error { return nil }

type NoopCIGatePublisher struct{}

func (NoopCIGatePublisher) PublishCIGate(context.Context, Result) error { return nil }

type AuditLogger interface {
	LogGateResult(ctx context.Context, run db.ReviewRun, result Result) error
}

type Service struct {
	statusPublisher StatusPublisher
	ciPublisher     CIGatePublisher
	auditLogger     AuditLogger
}

type Result struct {
	RunID                int64
	MergeRequestID       int64
	ProjectID            int64
	HeadSHA              string
	State                string
	Mode                 string
	BlockingFindings     int
	QualifyingFindingIDs []int64
	Summary              string
	Source               string
	TraceID              string
}

func NewService(status StatusPublisher, ci CIGatePublisher, audit AuditLogger) *Service {
	return &Service{statusPublisher: status, ciPublisher: ci, auditLogger: audit}
}

func ComputeResult(run db.ReviewRun, policy *db.ProjectPolicy, findings []db.ReviewFinding, traceID string) Result {
	mode := "threads_resolved"
	if policy != nil && strings.TrimSpace(policy.GateMode) != "" {
		mode = strings.TrimSpace(policy.GateMode)
	}
	qualifyingIDs := make([]int64, 0)
	for _, finding := range findings {
		if qualifies(policy, mode, finding) {
			qualifyingIDs = append(qualifyingIDs, finding.ID)
		}
	}
	state := "passed"
	if len(qualifyingIDs) > 0 {
		state = "failed"
	}
	return Result{RunID: run.ID, MergeRequestID: run.MergeRequestID, ProjectID: run.ProjectID, HeadSHA: run.HeadSha, State: state, Mode: mode, BlockingFindings: len(qualifyingIDs), QualifyingFindingIDs: qualifyingIDs, Summary: fmt.Sprintf("gate %s with %d qualifying findings", state, len(qualifyingIDs)), Source: "review_run", TraceID: traceID}
}

func (s *Service) Publish(ctx context.Context, result Result) error {
	if s == nil {
		return nil
	}
	if s.statusPublisher != nil {
		if err := s.statusPublisher.PublishStatus(ctx, result); err != nil {
			return err
		}
	}
	if s.ciPublisher != nil {
		if err := s.ciPublisher.PublishCIGate(ctx, result); err != nil {
			return err
		}
	}
	if s.auditLogger != nil {
		run := db.ReviewRun{ID: result.RunID}
		if err := s.auditLogger.LogGateResult(ctx, run, result); err != nil {
			return err
		}
	}
	return nil
}

func qualifies(policy *db.ProjectPolicy, mode string, finding db.ReviewFinding) bool {
	state := strings.ToLower(strings.TrimSpace(finding.State))
	if state == "ignored" || state == "fixed" || state == "stale" || state == "superseded" || state == "filtered" {
		return false
	}
	threshold := "low"
	if policy != nil && strings.TrimSpace(policy.SeverityThreshold) != "" {
		threshold = strings.TrimSpace(policy.SeverityThreshold)
	}
	if severityRank(finding.Severity) < severityRank(threshold) {
		return false
	}
	if policy != nil && policy.ConfidenceThreshold > 0 && finding.Confidence < policy.ConfidenceThreshold {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(mode), "threads_resolved") && !blocksThreadsResolved(finding) {
		return false
	}
	return true
}

func blocksThreadsResolved(finding db.ReviewFinding) bool {
	if strings.TrimSpace(finding.GitlabDiscussionID) == "" {
		return false
	}
	return !discussionResolved(finding)
}

func discussionResolved(finding db.ReviewFinding) bool {
	if text, ok := nullableStringValue(finding.Evidence); ok {
		if resolved, found := parseResolvedFlag(text); found {
			return resolved
		}
	}
	if text, ok := nullableStringValue(finding.BodyMarkdown); ok {
		if resolved, found := parseResolvedFlag(text); found {
			return resolved
		}
	}
	return false
}

func nullableStringValue(value sql.NullString) (string, bool) {
	if !value.Valid {
		return "", false
	}
	return value.String, true
}

func parseResolvedFlag(raw string) (bool, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, false
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err == nil {
		if resolved, found := resolvedFromValue(payload); found {
			return resolved, true
		}
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "resolved=true") || strings.Contains(lower, "\"resolved\":true"):
		return true, true
	case strings.Contains(lower, "resolved=false") || strings.Contains(lower, "\"resolved\":false"):
		return false, true
	default:
		return false, false
	}
}

func resolvedFromValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"resolved", "discussion_resolved", "thread_resolved"} {
			if raw, ok := typed[key]; ok {
				return coerceBool(raw)
			}
		}
		for _, key := range []string{"discussion", "thread", "gitlab_discussion", "metadata"} {
			if nested, ok := typed[key]; ok {
				if resolved, found := resolvedFromValue(nested); found {
					return resolved, true
				}
			}
		}
	case []any:
		for _, item := range typed {
			if resolved, found := resolvedFromValue(item); found {
				return resolved, true
			}
		}
	}
	return false, false
}

func coerceBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, false
		}
		return parsed, true
	case json.Number:
		intValue, err := typed.Int64()
		if err != nil {
			return false, false
		}
		return intValue != 0, true
	case float64:
		return typed != 0, true
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	default:
		return false, false
	}
}

func severityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

type DBAuditLogger struct {
	insert func(context.Context, db.InsertAuditLogParams) error
}

func NewDBAuditLogger(queries *db.Queries) *DBAuditLogger {
	return &DBAuditLogger{insert: func(ctx context.Context, arg db.InsertAuditLogParams) error {
		_, err := queries.InsertAuditLog(ctx, arg)
		return err
	}}
}

func (l *DBAuditLogger) LogGateResult(ctx context.Context, run db.ReviewRun, result Result) error {
	if l == nil || l.insert == nil {
		return nil
	}
	detail, _ := json.Marshal(map[string]any{"state": result.State, "mode": result.Mode, "blocking_findings": result.BlockingFindings, "qualifying_finding_ids": result.QualifyingFindingIDs, "trace_id": result.TraceID})
	return l.insert(ctx, db.InsertAuditLogParams{EntityType: "review_run", EntityID: run.ID, Action: "gate_published", Actor: "system", Detail: detail})
}
