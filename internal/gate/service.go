package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
)

type StatusPublisher interface {
	PublishStatus(ctx context.Context, result Result) error
}

type CIGatePublisher interface {
	PublishCIGate(ctx context.Context, result Result) error
}

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
		if qualifies(policy, finding) {
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

func qualifies(policy *db.ProjectPolicy, finding db.ReviewFinding) bool {
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
	return true
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
