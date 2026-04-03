package llm

import (
	"context"
	"strings"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

type SchemaIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type SchemaAttemptReport struct {
	Valid                   bool          `json:"valid"`
	Issues                  []SchemaIssue `json:"issues,omitempty"`
	ParseStage              string        `json:"parse_stage,omitempty"`
	MissingStructuredOutput bool          `json:"missing_structured_output,omitempty"`
}

type SchemaExecutionReport struct {
	Initial         SchemaAttemptReport  `json:"initial"`
	Repair          *SchemaAttemptReport `json:"repair,omitempty"`
	RepairAttempted bool                 `json:"repair_attempted"`
	FinalValid      bool                 `json:"final_valid"`
	FinalParseStage string               `json:"final_parse_stage,omitempty"`
}

type StructuredOutputCandidate struct {
	RawText                  string
	MissingStructuredOutput  error
	MissingStructuredRawText string
}

type ReviewSchemaHarnessResult struct {
	Result        ReviewResult
	RawText       string
	FallbackStage string
	Report        SchemaExecutionReport
	Tokens        int64
	Latency       time.Duration
}

type ReviewRepairFunc func(context.Context, string) (string, int64, time.Duration, error)

type ReviewSchemaHarness struct{}

func (ReviewSchemaHarness) Execute(ctx context.Context, request ctxpkg.ReviewRequest, candidate StructuredOutputCandidate, repair ReviewRepairFunc) (ReviewSchemaHarnessResult, error) {
	report := SchemaExecutionReport{}
	if candidate.MissingStructuredOutput != nil {
		report.Initial.MissingStructuredOutput = true
		report.Initial.Issues = []SchemaIssue{{
			Path:    "$",
			Message: candidate.MissingStructuredOutput.Error(),
		}}
		raw := strings.TrimSpace(candidate.MissingStructuredRawText)
		if raw == "" {
			raw = strings.TrimSpace(candidate.RawText)
		}
		if raw != "" {
			if result, parseStage, ok := salvageStructuredMissRaw(raw); ok {
				report.FinalValid = true
				report.FinalParseStage = parseStage
				return ReviewSchemaHarnessResult{
					Result:        result,
					RawText:       raw,
					FallbackStage: fallbackStageWithParseStage(structuredMissFallbackStage(candidate.MissingStructuredOutput), parseStage),
					Report:        report,
				}, nil
			}
		}
		if raw == "" || repair == nil {
			return ReviewSchemaHarnessResult{}, &providerParseError{
				cause:        candidate.MissingStructuredOutput,
				rawResponse:  raw,
				schemaReport: &report,
			}
		}
		return executeRepair(ctx, request, raw, candidate.MissingStructuredOutput, report.Initial.Issues, repair, report)
	}

	initialIssues, initialErr := validateReviewResultStrictIssues(candidate.RawText)
	report.Initial.Valid = initialErr == nil
	report.Initial.Issues = initialIssues
	if initialErr == nil {
		result, parseStage, parseErr := ParseReviewResult(candidate.RawText)
		if parseErr != nil {
			return ReviewSchemaHarnessResult{}, &providerParseError{
				cause:        parseErr,
				rawResponse:  candidate.RawText,
				schemaReport: &report,
			}
		}
		report.Initial.ParseStage = parseStage
		report.FinalValid = true
		report.FinalParseStage = parseStage
		return ReviewSchemaHarnessResult{
			Result:        result,
			RawText:       candidate.RawText,
			FallbackStage: parseStage,
			Report:        report,
		}, nil
	}
	if repair == nil {
		return ReviewSchemaHarnessResult{}, &providerParseError{
			cause:        initialErr,
			rawResponse:  candidate.RawText,
			schemaReport: &report,
		}
	}
	return executeRepair(ctx, request, candidate.RawText, initialErr, initialIssues, repair, report)
}

func executeRepair(ctx context.Context, request ctxpkg.ReviewRequest, raw string, repairReason error, repairIssues []SchemaIssue, repair ReviewRepairFunc, report SchemaExecutionReport) (ReviewSchemaHarnessResult, error) {
	if err := ctx.Err(); err != nil {
		return ReviewSchemaHarnessResult{}, err
	}
	repairRaw, repairTokens, repairLatency, repairErr := repair(ctx, buildReviewRepairPayload(request, raw, repairReason, repairIssues))
	report.RepairAttempted = true
	if repairErr != nil {
		return ReviewSchemaHarnessResult{}, &providerParseError{
			cause:        repairErr,
			rawResponse:  raw,
			schemaReport: &report,
		}
	}

	repairIssues, repairValidationErr := validateReviewResultStrictIssues(repairRaw)
	report.Repair = &SchemaAttemptReport{
		Valid:  repairValidationErr == nil,
		Issues: repairIssues,
	}
	if repairValidationErr == nil {
		result, parseStage, parseErr := ParseReviewResult(repairRaw)
		if parseErr != nil {
			return ReviewSchemaHarnessResult{}, &providerParseError{
				cause:        parseErr,
				rawResponse:  repairRaw,
				schemaReport: &report,
			}
		}
		report.Repair.ParseStage = parseStage
		report.FinalValid = true
		report.FinalParseStage = parseStage
		return ReviewSchemaHarnessResult{
			Result:        result,
			RawText:       repairRaw,
			FallbackStage: fallbackStageWithParseStage("repair_retry", parseStage),
			Report:        report,
			Tokens:        repairTokens,
			Latency:       repairLatency,
		}, nil
	}

	result, parseStage, parseErr := ParseReviewResult(repairRaw)
	if parseErr == nil {
		report.Repair.ParseStage = parseStage
		report.FinalValid = false
		report.FinalParseStage = parseStage
		return ReviewSchemaHarnessResult{
			Result:        result,
			RawText:       repairRaw,
			FallbackStage: fallbackStageWithParseStage("repair_retry", parseStage),
			Report:        report,
			Tokens:        repairTokens,
			Latency:       repairLatency,
		}, nil
	}

	return ReviewSchemaHarnessResult{}, &providerParseError{
		cause:        strictFailureCause(repairRaw, repairValidationErr),
		rawResponse:  repairRaw,
		schemaReport: &report,
	}
}

func strictFailureCause(raw string, validationErr error) error {
	_, err := salvageReviewResultAfterStrictValidationFailure(raw, validationErr)
	if err != nil {
		return err
	}
	return validationErr
}

func salvageStructuredMissRaw(raw string) (ReviewResult, string, bool) {
	result, parseStage, parseErr := ParseReviewResult(raw)
	if parseErr != nil {
		return ReviewResult{}, "", false
	}
	return result, parseStage, true
}

func structuredMissFallbackStage(err error) string {
	if err == nil {
		return "structured_output_miss"
	}
	if strings.Contains(strings.ToLower(err.Error()), "tool_use") {
		return "missing_tool_use"
	}
	return "structured_output_miss"
}
