package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

func TestValidateReviewResultStrictIssuesRecordsExactPaths(t *testing.T) {
	raw := `{"schema_version":"1.0","review_run_id":"rr-1","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","path":"main.go","anchor_kind":"new_line"}]}`

	issues, err := validateReviewResultStrictIssues(raw)
	if err == nil {
		t.Fatal("expected strict validation error")
	}
	if len(issues) == 0 {
		t.Fatal("expected structured issues")
	}
	found := false
	for _, issue := range issues {
		if issue.Path == "$.findings[0].body_markdown" {
			found = true
			if issue.Message == "" {
				t.Fatal("issue message should not be empty")
			}
		}
	}
	if !found {
		t.Fatalf("expected missing body_markdown issue, got %#v", issues)
	}
}

func TestReviewSchemaHarnessRepairsRecoverableStructuredMiss(t *testing.T) {
	harness := ReviewSchemaHarness{}
	request := ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "rr-1"}
	candidate := StructuredOutputCandidate{
		RawText:                  "I found one issue, please convert this into the required tool output.",
		MissingStructuredOutput:  errors.New("llm: missing tool_use block \"submit_review\""),
		MissingStructuredRawText: "I found one issue, please convert this into the required tool output.",
	}

	var repairPayload string
	result, err := harness.Execute(context.Background(), request, candidate, func(payload string) (string, int64, time.Duration, error) {
		repairPayload = payload
		return `{"schema_version":"1.0","review_run_id":"rr-1","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new_line","new_line":5}]}`, 17, 3 * time.Millisecond, nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if repairPayload == "" {
		t.Fatal("expected repair payload")
	}
	if !result.Report.RepairAttempted {
		t.Fatal("expected repair_attempted")
	}
	if !result.Report.Initial.MissingStructuredOutput {
		t.Fatal("expected initial missing structured output marker")
	}
	if result.Report.Repair == nil || !result.Report.Repair.Valid {
		t.Fatalf("repair report = %#v, want valid repair", result.Report.Repair)
	}
	if !result.Report.FinalValid {
		t.Fatal("expected final valid result")
	}
	if result.FallbackStage != "repair_retry" {
		t.Fatalf("fallback stage = %q, want repair_retry", result.FallbackStage)
	}
	if result.Result.ReviewRunID != "rr-1" {
		t.Fatalf("review_run_id = %q, want rr-1", result.Result.ReviewRunID)
	}
}

func TestReviewSchemaHarnessRepairPayloadIncludesStructuredIssues(t *testing.T) {
	harness := ReviewSchemaHarness{}
	request := ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "rr-structured"}
	candidate := StructuredOutputCandidate{
		RawText: `{"schema_version":"1.0","review_run_id":"rr-structured","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","path":"main.go","anchor_kind":"new_line","new_line":5}]}`,
	}

	var repairPayload map[string]any
	_, err := harness.Execute(context.Background(), request, candidate, func(payload string) (string, int64, time.Duration, error) {
		if unmarshalErr := json.Unmarshal([]byte(payload), &repairPayload); unmarshalErr != nil {
			t.Fatalf("unmarshal repair payload: %v", unmarshalErr)
		}
		return `{"schema_version":"1.0","review_run_id":"rr-structured","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new_line","new_line":5}]}`, 5, time.Millisecond, nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	issues, ok := repairPayload["validation_issues"].([]any)
	if !ok {
		t.Fatalf("validation_issues = %#v", repairPayload["validation_issues"])
	}
	if len(issues) == 0 {
		t.Fatal("expected validation_issues in repair payload")
	}
}

func TestReviewSchemaHarnessReturnsStructuredFailureReportAfterInvalidRepair(t *testing.T) {
	harness := ReviewSchemaHarness{}
	request := ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "rr-2"}
	candidate := StructuredOutputCandidate{
		RawText: `{"schema_version":"1.0","review_run_id":"rr-2","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","anchor_kind":"new_line","new_line":5}]}`,
	}

	_, err := harness.Execute(context.Background(), request, candidate, func(string) (string, int64, time.Duration, error) {
		return `{"schema_version":"1.0","review_run_id":"rr-2","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","anchor_kind":"new_line","new_line":5}]}`, 9, time.Millisecond, nil
	})
	if err == nil {
		t.Fatal("expected repair failure")
	}
	var parseErr *providerParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("error = %T, want providerParseError", err)
	}
	if parseErr.schemaReport == nil {
		t.Fatal("expected schema report on parse error")
	}
	if !parseErr.schemaReport.RepairAttempted {
		t.Fatal("expected repair_attempted in failure report")
	}
	if parseErr.schemaReport.Repair == nil || parseErr.schemaReport.Repair.Valid {
		t.Fatalf("repair report = %#v, want invalid repair", parseErr.schemaReport.Repair)
	}
	if parseErr.schemaReport.FinalValid {
		t.Fatal("expected final_valid=false")
	}
	if len(parseErr.schemaReport.Repair.Issues) == 0 {
		t.Fatal("expected repair issues")
	}
}
