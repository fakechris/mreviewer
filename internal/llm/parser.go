package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

type providerParseError struct {
	cause        error
	rawResponse  string
	latency      time.Duration
	tokens       int64
	model        string
	schemaReport *SchemaExecutionReport
}

type structuredOutputMissError struct {
	cause       error
	rawResponse string
}

func (e *providerParseError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.rawResponse) == "" {
		return e.cause.Error()
	}
	return fmt.Sprintf("%v: raw=%q", e.cause, e.rawResponse)
}

func (e *providerParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *structuredOutputMissError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *structuredOutputMissError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func enrichProviderParseError(err error, fallbackRaw string, latency time.Duration, tokens int64, model string) *providerParseError {
	var parseErr *providerParseError
	if errors.As(err, &parseErr) {
		raw := strings.TrimSpace(parseErr.rawResponse)
		if raw == "" {
			raw = strings.TrimSpace(fallbackRaw)
		}
		cause := parseErr.cause
		if cause == nil {
			cause = err
		}
		return &providerParseError{
			cause:        cause,
			rawResponse:  raw,
			latency:      latency + parseErr.latency,
			tokens:       tokens + parseErr.tokens,
			model:        model,
			schemaReport: parseErr.schemaReport,
		}
	}
	return &providerParseError{
		cause:       err,
		rawResponse: strings.TrimSpace(fallbackRaw),
		latency:     latency,
		tokens:      tokens,
		model:       model,
	}
}

func ParseReviewResult(raw string) (ReviewResult, string, error) {
	stages := []struct {
		name string
		fn   func(string) (string, bool)
	}{
		{name: "direct", fn: func(input string) (string, bool) { return input, strings.TrimSpace(input) != "" }},
		{name: "quoted_json_string", fn: unquoteJSONString},
		{name: "marker_extraction", fn: extractMarkedJSON},
		{name: "tolerant_repair", fn: tolerantRepairJSON},
	}
	for _, stage := range stages {
		candidate, ok := stage.fn(raw)
		if !ok {
			continue
		}
		var result ReviewResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil {
			if err := validateReviewResult(result); err != nil {
				normalized, normErr := parseReviewResultWithAliases(candidate)
				if normErr != nil {
					continue
				}
				if normalized.Status == "" {
					normalized.Status = "completed"
				}
				return normalized, stage.name, nil
			}
			if result.Status == "" {
				result.Status = "completed"
			}
			return result, stage.name, nil
		}
		normalized, err := parseReviewResultWithAliases(candidate)
		if err == nil {
			if normalized.Status == "" {
				normalized.Status = "completed"
			}
			return normalized, stage.name, nil
		}
	}
	return ReviewResult{}, "", fmt.Errorf("llm: unable to parse provider response")
}

func parseReviewResultWithAliases(raw string) (ReviewResult, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return ReviewResult{}, err
	}
	result := ReviewResult{
		SchemaVersion: stringAlias(payload, "schema_version"),
		ReviewRunID:   stringAlias(payload, "review_run_id"),
		Summary:       stringAlias(payload, "summary"),
		Status:        stringAlias(payload, "status"),
		BlindSpots:    stringSliceAlias(payload, "blind_spots"),
	}
	if summaryNote, ok := payload["summary_note"].(map[string]any); ok {
		body := stringAlias(summaryNote, "body_markdown")
		if body != "" {
			result.SummaryNote = &SummaryNote{BodyMarkdown: body}
		}
	}
	findings, ok := payload["findings"].([]any)
	if ok {
		result.Findings = make([]ReviewFinding, 0, len(findings))
		for _, item := range findings {
			findingMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			newLine := int32Alias(findingMap, "new_line", "line_start", "line")
			oldLine := int32Alias(findingMap, "old_line")
			rangeStartKind := stringAlias(findingMap, "range_start_kind")
			rangeStartOldLine := int32Alias(findingMap, "range_start_old_line")
			rangeStartNewLine := int32Alias(findingMap, "range_start_new_line")
			rangeEndKind := stringAlias(findingMap, "range_end_kind")
			rangeEndOldLine := int32Alias(findingMap, "range_end_old_line")
			rangeEndNewLine := int32Alias(findingMap, "range_end_new_line")
			anchorKind := stringAlias(findingMap, "anchor_kind")
			if anchorKind == "" && newLine != nil {
				anchorKind = "new_line"
			}
			if anchorKind == "" && oldLine != nil {
				anchorKind = "old_line"
			}
			category := stringAlias(findingMap, "category", "type")
			if category == "" {
				category = "code_defect"
			}
			result.Findings = append(result.Findings, ReviewFinding{
				Category:               category,
				Severity:               stringAlias(findingMap, "severity"),
				Confidence:             float64Alias(findingMap, "confidence"),
				Title:                  stringAlias(findingMap, "title", "description"),
				BodyMarkdown:           stringAlias(findingMap, "body_markdown", "body", "description"),
				Path:                   stringAlias(findingMap, "path", "file_path", "file"),
				AnchorKind:             anchorKind,
				OldLine:                oldLine,
				NewLine:                newLine,
				RangeStartKind:         rangeStartKind,
				RangeStartOldLine:      rangeStartOldLine,
				RangeStartNewLine:      rangeStartNewLine,
				RangeEndKind:           rangeEndKind,
				RangeEndOldLine:        rangeEndOldLine,
				RangeEndNewLine:        rangeEndNewLine,
				AnchorSnippet:          stringAlias(findingMap, "anchor_snippet"),
				Evidence:               stringSliceAlias(findingMap, "evidence"),
				SuggestedPatch:         stringAlias(findingMap, "suggested_patch", "actionable_fix", "suggested_action"),
				CanonicalKey:           stringAlias(findingMap, "canonical_key"),
				Symbol:                 stringAlias(findingMap, "symbol"),
				TriggerCondition:       stringAlias(findingMap, "trigger_condition"),
				Impact:                 stringAlias(findingMap, "impact"),
				IntroducedByThisChange: boolAlias(findingMap, "introduced_by_this_change"),
				BlindSpots:             stringSliceAlias(findingMap, "blind_spots"),
				NoFindingReason:        stringAlias(findingMap, "no_finding_reason"),
			})
		}
	}
	if err := validateReviewResult(result); err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

func stringAlias(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func stringSliceAlias(payload map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if text != "" {
				return []string{text}
			}
		case []any:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				text, ok := item.(string)
				if !ok {
					continue
				}
				text = strings.TrimSpace(text)
				if text != "" {
					items = append(items, text)
				}
			}
			if len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func float64Alias(payload map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func int32Alias(payload map[string]any, keys ...string) *int32 {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if parsed, ok := coerceInt32(value); ok {
			return &parsed
		}
	}
	return nil
}

func coerceInt32(value any) (int32, bool) {
	switch typed := value.(type) {
	case float64:
		if math.Trunc(typed) != typed || typed < math.MinInt32 || typed > math.MaxInt32 {
			return 0, false
		}
		return int32(typed), true
	case json.Number:
		raw := strings.TrimSpace(typed.String())
		if raw == "" || strings.ContainsAny(raw, ".eE") {
			return 0, false
		}
		parsed, ok := new(big.Int).SetString(raw, 10)
		if !ok || !parsed.IsInt64() {
			return 0, false
		}
		value := parsed.Int64()
		if value < math.MinInt32 || value > math.MaxInt32 {
			return 0, false
		}
		return int32(value), true
	default:
		return 0, false
	}
}

func boolAlias(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if typed, ok := value.(bool); ok {
			return typed
		}
	}
	return false
}

func validateReviewResult(result ReviewResult) error {
	if strings.TrimSpace(result.SchemaVersion) == "" {
		return fmt.Errorf("missing schema_version")
	}
	if strings.TrimSpace(result.ReviewRunID) == "" {
		return fmt.Errorf("missing review_run_id")
	}
	if result.Status == parserErrorCode {
		if result.SummaryNote == nil || strings.TrimSpace(result.SummaryNote.BodyMarkdown) == "" {
			return fmt.Errorf("missing summary_note for parser_error")
		}
		return nil
	}
	for i, finding := range result.Findings {
		if strings.TrimSpace(finding.Category) == "" || strings.TrimSpace(finding.Severity) == "" || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.AnchorKind) == "" {
			return fmt.Errorf("finding %d missing required fields", i)
		}
		if strings.TrimSpace(finding.Path) == "" && !findingAllowsEmptyPath(finding) {
			return fmt.Errorf("finding %d missing required fields", i)
		}
	}
	return nil
}

func findingAllowsEmptyPath(finding ReviewFinding) bool {
	if strings.TrimSpace(finding.Path) != "" {
		return false
	}
	if finding.OldLine != nil || finding.NewLine != nil {
		return false
	}
	if finding.RangeStartOldLine != nil || finding.RangeStartNewLine != nil || finding.RangeEndOldLine != nil || finding.RangeEndNewLine != nil {
		return false
	}
	return true
}

func summaryResultSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"review_run_id":  map[string]any{"type": "string"},
			"walkthrough":    map[string]any{"type": "string"},
			"risk_areas": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"severity":    map[string]any{"type": "string"},
					},
					"required":             []string{"path", "description", "severity"},
					"additionalProperties": false,
				},
			},
			"blind_spots": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"verdict":     map[string]any{"type": "string"},
		},
		"required":             []string{"schema_version", "review_run_id", "walkthrough", "verdict"},
		"additionalProperties": false,
	}
}

func ParseSummaryResult(raw string) (SummaryResult, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return SummaryResult{}, fmt.Errorf("llm: empty summary response")
	}
	for _, extract := range []func(string) (string, bool){
		func(s string) (string, bool) { return s, s != "" },
		extractMarkedJSON,
		tolerantRepairJSON,
	} {
		text, ok := extract(candidate)
		if !ok {
			continue
		}
		var result SummaryResult
		if err := json.Unmarshal([]byte(text), &result); err == nil {
			if strings.TrimSpace(result.SchemaVersion) != "" && strings.TrimSpace(result.ReviewRunID) != "" && strings.TrimSpace(result.Walkthrough) != "" && strings.TrimSpace(result.Verdict) != "" {
				return result, nil
			}
		}
	}
	return SummaryResult{}, fmt.Errorf("llm: unable to parse summary response")
}

func reviewResultSchema() map[string]any {
	return reviewResultSchemaWithFindingItem(reviewFindingSchema())
}

func reviewResultSchemaAnthropicCompact() map[string]any {
	return reviewResultSchemaWithFindingItem(reviewFindingSchemaAnthropicCompact())
}

func reviewResultSchemaWithFindingItem(findingItem map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"review_run_id":  map[string]any{"type": "string"},
			"status":         map[string]any{"type": "string"},
			"summary":        map[string]any{"type": "string"},
			"summary_note": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"body_markdown": map[string]any{"type": "string"}},
				"required":             []string{"body_markdown"},
				"additionalProperties": false,
			},
			"blind_spots": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"findings":    map[string]any{"type": "array", "items": findingItem},
		},
		"required":             []string{"schema_version", "review_run_id", "summary", "findings"},
		"additionalProperties": false,
	}
}

var reviewFindingRequiredFields = []string{
	"category",
	"severity",
	"confidence",
	"title",
	"body_markdown",
	"path",
	"anchor_kind",
}

func reviewFindingSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":                  map[string]any{"type": "string"},
			"severity":                  map[string]any{"type": "string"},
			"confidence":                map[string]any{"type": "number"},
			"title":                     map[string]any{"type": "string"},
			"body_markdown":             map[string]any{"type": "string"},
			"path":                      map[string]any{"type": "string"},
			"anchor_kind":               map[string]any{"type": "string"},
			"old_line":                  map[string]any{"type": "integer"},
			"new_line":                  map[string]any{"type": "integer"},
			"range_start_kind":          map[string]any{"type": "string"},
			"range_start_old_line":      map[string]any{"type": "integer"},
			"range_start_new_line":      map[string]any{"type": "integer"},
			"range_end_kind":            map[string]any{"type": "string"},
			"range_end_old_line":        map[string]any{"type": "integer"},
			"range_end_new_line":        map[string]any{"type": "integer"},
			"anchor_snippet":            map[string]any{"type": "string"},
			"evidence":                  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"suggested_patch":           map[string]any{"type": "string"},
			"canonical_key":             map[string]any{"type": "string"},
			"symbol":                    map[string]any{"type": "string"},
			"trigger_condition":         map[string]any{"type": "string"},
			"impact":                    map[string]any{"type": "string"},
			"introduced_by_this_change": map[string]any{"type": "boolean"},
			"blind_spots":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"no_finding_reason":         map[string]any{"type": "string"},
		},
		"required":             append([]string(nil), reviewFindingRequiredFields...),
		"additionalProperties": false,
	}
}

func reviewFindingSchemaAnthropicCompact() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":                  map[string]any{"type": "string"},
			"severity":                  map[string]any{"type": "string"},
			"confidence":                map[string]any{"type": "number"},
			"title":                     map[string]any{"type": "string"},
			"body_markdown":             map[string]any{"type": "string"},
			"path":                      map[string]any{"type": "string"},
			"anchor_kind":               map[string]any{"type": "string"},
			"old_line":                  map[string]any{"type": "integer"},
			"new_line":                  map[string]any{"type": "integer"},
			"canonical_key":             map[string]any{"type": "string"},
			"symbol":                    map[string]any{"type": "string"},
			"introduced_by_this_change": map[string]any{"type": "boolean"},
		},
		"required":             append([]string(nil), reviewFindingRequiredFields...),
		"additionalProperties": false,
	}
}

func reviewResultSchemaOpenAIStrict() map[string]any {
	schema, _ := transformJSONSchemaValue(reviewResultSchema(), false, true).(map[string]any)
	return schema
}

func reviewResultValidationSchema() map[string]any {
	schema, _ := transformJSONSchemaValue(reviewResultSchema(), false, false).(map[string]any)
	return schema
}

func transformJSONSchemaValue(value any, nullable bool, requireAll bool) any {
	switch typed := value.(type) {
	case map[string]any:
		return transformJSONSchemaObject(typed, nullable, requireAll)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, transformJSONSchemaValue(item, false, requireAll))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		out = append(out, typed...)
		return out
	default:
		return typed
	}
}

func transformJSONSchemaObject(schema map[string]any, nullable bool, requireAll bool) map[string]any {
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		out[key] = transformJSONSchemaValue(value, false, requireAll)
	}

	switch schemaType := schemaTypeName(out); schemaType {
	case "object":
		props, _ := out["properties"].(map[string]any)
		requiredSet := make(map[string]struct{}, len(anyToStringSlice(out["required"])))
		for _, key := range anyToStringSlice(out["required"]) {
			requiredSet[key] = struct{}{}
		}
		keys := make([]string, 0, len(props))
		for key, rawProp := range props {
			propSchema, ok := rawProp.(map[string]any)
			if !ok {
				continue
			}
			_, required := requiredSet[key]
			props[key] = transformJSONSchemaObject(propSchema, !required, requireAll)
			keys = append(keys, key)
		}
		out["properties"] = props
		if requireAll {
			sort.Strings(keys)
			out["required"] = keys
		}
	case "array":
		itemSchema, ok := out["items"].(map[string]any)
		if ok {
			out["items"] = transformJSONSchemaObject(itemSchema, false, requireAll)
		}
	}

	if nullable {
		out["type"] = appendSchemaType(out["type"], "null")
	}
	return out
}

func schemaTypeName(schema map[string]any) string {
	switch typed := schema["type"].(type) {
	case string:
		return typed
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "null" {
				return text
			}
		}
	case []string:
		for _, item := range typed {
			if item != "null" {
				return item
			}
		}
	}
	return ""
}

func appendSchemaType(value any, schemaType string) []any {
	switch typed := value.(type) {
	case string:
		if typed == schemaType {
			return []any{typed}
		}
		return []any{typed, schemaType}
	case []any:
		out := make([]any, 0, len(typed)+1)
		seen := false
		for _, item := range typed {
			out = append(out, item)
			if text, ok := item.(string); ok && text == schemaType {
				seen = true
			}
		}
		if !seen {
			out = append(out, schemaType)
		}
		return out
	case []string:
		out := make([]any, 0, len(typed)+1)
		seen := false
		for _, item := range typed {
			out = append(out, item)
			if item == schemaType {
				seen = true
			}
		}
		if !seen {
			out = append(out, schemaType)
		}
		return out
	default:
		return []any{schemaType}
	}
}

func extractMarkedJSON(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	start := strings.Index(trimmed, "```")
	if start >= 0 {
		body := trimmed[start+3:]
		body = strings.TrimPrefix(body, "json")
		end := strings.Index(body, "```")
		if end >= 0 {
			return strings.TrimSpace(body[:end]), true
		}
	}
	start = strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(trimmed[start : end+1]), true
	}
	return "", false
}

func unquoteJSONString(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	var decoded string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "", false
	}
	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return "", false
	}
	if !strings.HasPrefix(decoded, "{") && !strings.HasPrefix(decoded, "[") {
		return "", false
	}
	return decoded, true
}

func tolerantRepairJSON(raw string) (string, bool) {
	candidate, ok := extractMarkedJSON(raw)
	if !ok {
		candidate = strings.TrimSpace(raw)
	}
	if candidate == "" {
		return "", false
	}
	candidate = strings.ReplaceAll(candidate, "\t", " ")
	candidate = strings.ReplaceAll(candidate, ",]", "]")
	candidate = strings.ReplaceAll(candidate, ",}", "}")
	open := strings.Count(candidate, "{") - strings.Count(candidate, "}")
	for open > 0 {
		candidate += "}"
		open--
	}
	return candidate, true
}

func collectMessageText(message *anthropic.Message) string {
	if message == nil {
		return ""
	}
	var parts []string
	for _, block := range message.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			parts = append(parts, tb.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func collectToolUseInput(message *anthropic.Message, toolName string) (string, error) {
	if message == nil {
		return "", fmt.Errorf("llm: missing tool_use block %q", toolName)
	}
	for _, block := range message.Content {
		if tb, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			if strings.TrimSpace(tb.Name) != strings.TrimSpace(toolName) {
				continue
			}
			return strings.TrimSpace(string(tb.Input)), nil
		}
	}
	return "", fmt.Errorf("llm: missing tool_use block %q", toolName)
}

func buildReviewRepairPayload(request ctxpkg.ReviewRequest, invalidRaw string, validationErr error, validationIssues []SchemaIssue) string {
	payload := map[string]any{
		"task": "repair_review_output",
		"instructions": []string{
			"Call submit_review exactly once.",
			"Do not change the semantic meaning unless required to satisfy schema validation.",
			"Return tool input that strictly satisfies the required schema.",
			"Preserve every valid field from the original tool input.",
			"Fill every missing required field called out by validation_error.",
			"Use validation_issues for exact field paths when repairing the payload.",
			"Do not emit markdown fences or free-form prose.",
		},
		"original_request": request,
		"invalid_tool_input": func() any {
			var parsed any
			if err := json.Unmarshal([]byte(invalidRaw), &parsed); err != nil {
				return invalidRaw
			}
			return parsed
		}(),
		"validation_error":  validationErr.Error(),
		"validation_issues": validationIssues,
	}
	return mustJSON(payload)
}

func salvageReviewResultAfterStrictValidationFailure(raw string, validationErr error) (ReviewResult, error) {
	result, _, err := ParseReviewResult(raw)
	if err == nil {
		return result, nil
	}
	return ReviewResult{}, fmt.Errorf("llm: strict validation failed after repair: %w", validationErr)
}

func validateReviewResultStrictJSON(raw string) error {
	_, err := validateReviewResultStrictIssues(raw)
	return err
}

func validateReviewResultStrictIssues(raw string) ([]SchemaIssue, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		wrapped := fmt.Errorf("llm: strict validation decode failed: %w", err)
		return []SchemaIssue{{Path: "$", Message: wrapped.Error()}}, wrapped
	}
	if decoder.More() {
		err := fmt.Errorf("llm: strict validation found trailing JSON content")
		return []SchemaIssue{{Path: "$", Message: err.Error()}}, err
	}
	errs := validateValueAgainstSchema(value, reviewResultValidationSchema(), "$")
	if len(errs) == 0 {
		return nil, nil
	}
	issues := make([]SchemaIssue, 0, len(errs))
	for _, issue := range errs {
		issues = append(issues, schemaIssueFromValidationMessage(issue))
	}
	return issues, fmt.Errorf("llm: strict validation failed: %s", strings.Join(errs, "; "))
}

func schemaIssueFromValidationMessage(message string) SchemaIssue {
	message = strings.TrimSpace(message)
	if message == "" {
		return SchemaIssue{Path: "$", Message: "unknown validation error"}
	}
	if idx := strings.IndexByte(message, ' '); idx > 0 {
		return SchemaIssue{
			Path:    strings.TrimSpace(message[:idx]),
			Message: strings.TrimSpace(message[idx+1:]),
		}
	}
	return SchemaIssue{Path: "$", Message: message}
}

func validateValueAgainstSchema(value any, schema map[string]any, path string) []string {
	typ, _ := schema["type"].(string)
	if typ == "" {
		typ = schemaTypeName(schema)
	}
	if value == nil {
		if schemaAllowsNull(schema) {
			return nil
		}
		if typ == "" {
			return nil
		}
		return []string{fmt.Sprintf("%s must be %s", path, typ)}
	}
	switch typ {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s must be object", path)}
		}
		props, _ := schema["properties"].(map[string]any)
		required := anyToStringSlice(schema["required"])
		var errs []string
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for key := range obj {
				if _, allowed := props[key]; !allowed {
					errs = append(errs, fmt.Sprintf("%s.%s is not allowed", path, key))
				}
			}
		}
		for _, key := range required {
			if _, ok := obj[key]; !ok {
				errs = append(errs, fmt.Sprintf("%s.%s is required", path, key))
			}
		}
		for key, propValue := range obj {
			propSchema, ok := props[key].(map[string]any)
			if !ok {
				continue
			}
			errs = append(errs, validateValueAgainstSchema(propValue, propSchema, path+"."+key)...)
		}
		return errs
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s must be array", path)}
		}
		itemSchema, _ := schema["items"].(map[string]any)
		var errs []string
		for i, item := range arr {
			errs = append(errs, validateValueAgainstSchema(item, itemSchema, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return errs
	case "string":
		if _, ok := value.(string); !ok {
			return []string{fmt.Sprintf("%s must be string", path)}
		}
	case "number":
		switch value.(type) {
		case json.Number, float64, float32, int, int32, int64:
			return nil
		default:
			return []string{fmt.Sprintf("%s must be number", path)}
		}
	case "integer":
		switch value.(type) {
		case json.Number, float64, float32, int, int32, int64:
			return nil
		default:
			return []string{fmt.Sprintf("%s must be integer", path)}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s must be boolean", path)}
		}
	}
	return nil
}

func schemaAllowsNull(schema map[string]any) bool {
	switch typed := schema["type"].(type) {
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == "null" {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			if item == "null" {
				return true
			}
		}
	}
	return false
}

func anyToStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func isParserError(err error) bool {
	if err == nil {
		return false
	}
	var parseErr *providerParseError
	if errors.As(err, &parseErr) {
		return true
	}
	if strings.Contains(err.Error(), parserErrorCode) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unparseable")
}
