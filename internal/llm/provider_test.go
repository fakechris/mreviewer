package llm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/metrics"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	metrics2 "github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/reviewlang"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

const providerTestMigrationsDir = "../../migrations"

func TestMiniMaxRequestShape(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[]}}],"usage":{"input_tokens":8,"output_tokens":42}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "secret-token", Model: "MiniMax-M2.5", HTTPClient: &http.Client{Transport: transport}, Now: func() time.Time { return time.Unix(100, 0) }})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}
	request := ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123", Project: ctxpkg.ProjectContext{ProjectID: 1, FullPath: "group/proj"}, MergeRequest: ctxpkg.MergeRequestContext{IID: 7, Title: "Title"}, Version: ctxpkg.VersionContext{HeadSHA: "head"}, Rules: ctxpkg.TrustedRules{PlatformPolicy: "policy"}, Changes: []ctxpkg.Change{{Path: "main.go", Status: "modified", ChangedLines: 1, Hunks: []ctxpkg.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Patch: "@@ -1,1 +1,1 @@\n-a\n+b", ChangedLines: 1}}}}}
	response, err := provider.Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if response.Latency != 0 {
		t.Fatalf("latency = %v, want 0 with fixed clock", response.Latency)
	}
	if response.Tokens != 50 {
		t.Fatalf("tokens = %d, want 50", response.Tokens)
	}
	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["model"] != "MiniMax-M2.5" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if payload["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens = %#v", payload["max_tokens"])
	}
	if payload["temperature"] != 0.2 {
		t.Fatalf("temperature = %#v, want 0.2", payload["temperature"])
	}
	systemBlocks, ok := payload["system"].([]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("system = %#v, want one text block", payload["system"])
	}
	systemBlock, ok := systemBlocks[0].(map[string]any)
	if !ok {
		t.Fatalf("system block = %#v", systemBlocks[0])
	}
	if systemBlock["type"] != "text" {
		t.Fatalf("system block type = %#v, want text", systemBlock["type"])
	}
	if _, ok := payload["output_config"]; ok {
		t.Fatal("output_config should not be sent to MiniMax Anthropic-compatible endpoint")
	}
	if _, ok := payload["tools"]; !ok {
		t.Fatal("missing review tool declaration")
	}
	if _, ok := payload["tool_choice"]; !ok {
		t.Fatal("missing forced tool choice")
	}
	if got := transport.header.Get("X-Api-Key"); got != "secret-token" {
		t.Fatalf("x-api-key = %q", got)
	}
}

func TestMiniMaxSummaryRequestShape(t *testing.T) {
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL: "https://api.minimaxi.com/anthropic",
		APIKey:  "secret-token",
		Model:   "MiniMax-M2.5",
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	payload := provider.SummaryRequestPayloadWithSystemPrompt(
		ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"},
		"summary system prompt",
	)

	if payload["model"] != "MiniMax-M2.5" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if payload["temperature"] != 0.2 {
		t.Fatalf("temperature = %#v, want 0.2", payload["temperature"])
	}
	if _, ok := payload["system"]; !ok {
		t.Fatal("missing system prompt")
	}
	if _, ok := payload["output_config"]; ok {
		t.Fatal("output_config should not be sent to MiniMax Anthropic-compatible endpoint")
	}
}

func TestMiniMaxSummaryCallUsesTypedSystemBlockAndCountsTotalTokens(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"text","text":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"walkthrough\":\"ok\",\"verdict\":\"approve\"}"}],"usage":{"input_tokens":6,"output_tokens":19}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	response, err := provider.Summarize(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if response.Tokens != 25 {
		t.Fatalf("tokens = %d, want 25", response.Tokens)
	}
	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	systemBlocks, ok := payload["system"].([]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("system = %#v, want one text block", payload["system"])
	}
	systemBlock, ok := systemBlocks[0].(map[string]any)
	if !ok {
		t.Fatalf("system block = %#v", systemBlocks[0])
	}
	if systemBlock["type"] != "text" {
		t.Fatalf("system block type = %#v, want text", systemBlock["type"])
	}
}

func TestMiniMaxParserErrorIncludesRawSnippet(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":"not-json-response"}],"usage":{"output_tokens":7}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.5",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected parser error")
	}
	if !strings.Contains(err.Error(), "not-json-response") {
		t.Fatalf("parser error missing raw snippet: %v", err)
	}
}

func TestCanonicalRunStatus(t *testing.T) {
	tests := []struct {
		name         string
		modelStatus  string
		findingCount int
		want         string
	}{
		{name: "parser error preserved", modelStatus: parserErrorCode, findingCount: 0, want: parserErrorCode},
		{name: "empty status with no findings becomes completed", modelStatus: "", findingCount: 0, want: "completed"},
		{name: "requested changes with findings stays requested changes", modelStatus: "requested_changes", findingCount: 1, want: "requested_changes"},
		{name: "model failure with findings becomes requested changes", modelStatus: "failure", findingCount: 2, want: "requested_changes"},
		{name: "model failure without findings becomes completed", modelStatus: "failure", findingCount: 0, want: "completed"},
		{name: "completed with findings becomes requested changes", modelStatus: "completed", findingCount: 1, want: "requested_changes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalRunStatus(tt.modelStatus, tt.findingCount); got != tt.want {
				t.Fatalf("canonicalRunStatus(%q, %d) = %q, want %q", tt.modelStatus, tt.findingCount, got, tt.want)
			}
		})
	}
}

func TestMiniMaxToolCallRequestShape(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[]}}],"usage":{"output_tokens":42}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one submit_review tool", payload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool payload = %#v", tools[0])
	}
	if tool["name"] != "submit_review" {
		t.Fatalf("tool name = %#v, want submit_review", tool["name"])
	}
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema = %#v", tool["input_schema"])
	}
	if inputSchema["type"] != "object" {
		t.Fatalf("input_schema.type = %#v, want object", inputSchema["type"])
	}
	toolChoice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v", payload["tool_choice"])
	}
	if toolChoice["type"] != "tool" {
		t.Fatalf("tool_choice.type = %#v, want tool", toolChoice["type"])
	}
	if toolChoice["name"] != "submit_review" {
		t.Fatalf("tool_choice.name = %#v, want submit_review", toolChoice["name"])
	}
}

func TestAnthropicToolCallRequestShapeUsesCompactFindingSchema(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[]}}],"usage":{"output_tokens":42}}`}
	provider, err := NewAnthropicProvider(ProviderConfig{
		BaseURL:    "https://api.anthropic.com",
		APIKey:     "secret-token",
		Model:      "claude-opus-4-6",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewAnthropicProvider: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one submit_review tool", payload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool payload = %#v", tools[0])
	}
	if tool["strict"] != true {
		t.Fatalf("tool.strict = %#v, want true", tool["strict"])
	}
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema = %#v", tool["input_schema"])
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema.properties = %#v", inputSchema["properties"])
	}
	if _, ok := props["summary_note"]; !ok {
		t.Fatal("summary_note should remain available for anthropic schema")
	}
	if _, ok := props["blind_spots"]; !ok {
		t.Fatal("blind_spots should remain available for anthropic schema")
	}
	findings, ok := props["findings"].(map[string]any)
	if !ok {
		t.Fatalf("findings schema = %#v", props["findings"])
	}
	items, ok := findings["items"].(map[string]any)
	if !ok {
		t.Fatalf("findings.items = %#v", findings["items"])
	}
	if items["additionalProperties"] != false {
		t.Fatalf("findings.items.additionalProperties = %#v, want false", items["additionalProperties"])
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("findings.items.properties = %#v", items["properties"])
	}
	required, ok := items["required"].([]any)
	if !ok {
		t.Fatalf("findings.items.required = %#v, want array", items["required"])
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, value := range required {
		key, ok := value.(string)
		if !ok {
			t.Fatalf("findings.items.required contains non-string: %#v", value)
		}
		requiredSet[key] = struct{}{}
	}
	for _, key := range []string{"category", "severity", "confidence", "title", "body_markdown", "path", "anchor_kind"} {
		if _, ok := itemProps[key]; !ok {
			t.Fatalf("compact anthropic finding schema missing %q", key)
		}
		if _, ok := requiredSet[key]; !ok {
			t.Fatalf("compact anthropic finding schema should require %q", key)
		}
	}
	for _, key := range []string{"evidence", "blind_spots", "range_start_kind", "range_end_kind", "suggested_patch", "canonical_key"} {
		if _, ok := itemProps[key]; ok {
			t.Fatalf("compact anthropic finding schema should omit %q", key)
		}
	}
}

func TestReviewResultSchemaForProfile(t *testing.T) {
	full := reviewResultSchemaForProfile(anthropicToolProfile{kind: ProviderKindMiniMax})
	compact := reviewResultSchemaForProfile(anthropicToolProfile{kind: ProviderKindAnthropic})

	fullFindings, ok := full["properties"].(map[string]any)["findings"].(map[string]any)
	if !ok {
		t.Fatalf("full findings schema = %#v", full["properties"])
	}
	fullItems, ok := fullFindings["items"].(map[string]any)
	if !ok {
		t.Fatalf("full findings.items = %#v", fullFindings["items"])
	}
	fullItemProps, ok := fullItems["properties"].(map[string]any)
	if !ok {
		t.Fatalf("full findings.items.properties = %#v", fullItems["properties"])
	}
	if _, ok := fullItemProps["evidence"]; !ok {
		t.Fatal("full schema should retain evidence")
	}

	compactFindings, ok := compact["properties"].(map[string]any)["findings"].(map[string]any)
	if !ok {
		t.Fatalf("compact findings schema = %#v", compact["properties"])
	}
	compactItems, ok := compactFindings["items"].(map[string]any)
	if !ok {
		t.Fatalf("compact findings.items = %#v", compactFindings["items"])
	}
	compactItemProps, ok := compactItems["properties"].(map[string]any)
	if !ok {
		t.Fatalf("compact findings.items.properties = %#v", compactItems["properties"])
	}
	if _, ok := compactItemProps["evidence"]; ok {
		t.Fatal("compact schema should omit evidence")
	}
	if _, ok := compact["properties"].(map[string]any)["summary_note"]; !ok {
		t.Fatal("compact schema should retain summary_note")
	}
}

func TestMiniMaxToolCallResponseParsesToolInput(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}}],"usage":{"output_tokens":42}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	response, err := provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if response.Result.Summary != "ok" {
		t.Fatalf("summary = %q, want ok", response.Result.Summary)
	}
	if len(response.Result.Findings) != 1 || response.Result.Findings[0].Title != "Issue" {
		t.Fatalf("findings = %#v", response.Result.Findings)
	}
}

func TestMiniMaxToolCallMissingToolUseFails(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"text","text":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}],"usage":{"output_tokens":42}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected missing tool_use parser error")
	}
	if !isParserError(err) {
		t.Fatalf("error = %v, want parser_error classification", err)
	}
	if !strings.Contains(err.Error(), "tool_use") {
		t.Fatalf("error = %v, want tool_use mention", err)
	}
	var parseErr *providerParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected providerParseError, got %T", err)
	}
	if !strings.Contains(parseErr.rawResponse, `"findings":[]`) {
		t.Fatalf("rawResponse = %q, want captured plain-text payload", parseErr.rawResponse)
	}
}

func TestMiniMaxToolCallRepairsInvalidToolInput(t *testing.T) {
	transport := &captureTransport{responseBodies: []string{
		`{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[{"severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}}],"usage":{"output_tokens":42}}`,
		`{"id":"msg_2","content":[{"type":"tool_use","id":"toolu_2","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}}],"usage":{"output_tokens":21}}`,
	}}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	response, err := provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if transport.calls != 2 {
		t.Fatalf("calls = %d, want 2", transport.calls)
	}
	if response.FallbackStage != "repair_retry" {
		t.Fatalf("fallback stage = %q, want repair_retry", response.FallbackStage)
	}
	if len(response.Result.Findings) != 1 || response.Result.Findings[0].Category != "bug" {
		t.Fatalf("findings = %#v", response.Result.Findings)
	}
}

func TestMiniMaxToolCallFailsAfterInvalidRepairRetry(t *testing.T) {
	transport := &captureTransport{responseBodies: []string{
		`{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[{"severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}}],"usage":{"output_tokens":42}}`,
		`{"id":"msg_2","content":[{"type":"tool_use","id":"toolu_2","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[{"severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}}],"usage":{"output_tokens":21}}`,
	}}
	provider, err := NewMiniMaxProvider(ProviderConfig{
		BaseURL:    "https://api.minimaxi.com/anthropic",
		APIKey:     "secret-token",
		Model:      "MiniMax-M2.7",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected parser error after invalid repair retry")
	}
	if transport.calls != 2 {
		t.Fatalf("calls = %d, want 2", transport.calls)
	}
	if !strings.Contains(err.Error(), "strict validation") {
		t.Fatalf("error = %v, want strict validation mention", err)
	}
}

func TestBuildSummarySystemPromptRequiresStrictJSONOutput(t *testing.T) {
	prompt := buildSummarySystemPrompt(reviewlang.DefaultOutputLanguage)

	for _, want := range []string{
		"Return ONLY valid JSON.",
		"Do not wrap the JSON in markdown fences.",
		`Required top-level fields: schema_version, review_run_id, walkthrough, verdict.`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("summary prompt missing %q: %s", want, prompt)
		}
	}
}

func TestParseValidReviewResult(t *testing.T) {
	raw := `{"schema_version":"1.0","review_run_id":"rr-1","summary":"Looks good","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Nil dereference","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":12}]}`
	result, stage, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if stage != "direct" {
		t.Fatalf("stage = %q, want direct", stage)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Nil dereference" {
		t.Fatalf("unexpected findings: %#v", result.Findings)
	}
}

func TestParseReviewResultWithNewFields(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"rr-2",
		"summary":"Found one issue",
		"blind_spots":["concurrent access patterns not fully verified"],
		"findings":[{
			"category":"bug",
			"severity":"high",
			"confidence":0.95,
			"title":"Nil dereference",
			"body_markdown":"body",
			"path":"main.go",
			"anchor_kind":"new",
			"new_line":12,
			"trigger_condition":"pointer used without nil check on line 12",
			"impact":"runtime panic in production if input is nil",
			"introduced_by_this_change":true,
			"blind_spots":["untested error path"],
			"no_finding_reason":""
		}]
	}`
	result, stage, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if stage != "direct" {
		t.Fatalf("stage = %q, want direct", stage)
	}
	if len(result.BlindSpots) != 1 || result.BlindSpots[0] != "concurrent access patterns not fully verified" {
		t.Fatalf("result blind_spots = %#v", result.BlindSpots)
	}
	finding := result.Findings[0]
	if finding.TriggerCondition != "pointer used without nil check on line 12" {
		t.Fatalf("trigger_condition = %q", finding.TriggerCondition)
	}
	if finding.Impact != "runtime panic in production if input is nil" {
		t.Fatalf("impact = %q", finding.Impact)
	}
	if !finding.IntroducedByThisChange {
		t.Fatal("introduced_by_this_change = false, want true")
	}
	if len(finding.BlindSpots) != 1 || finding.BlindSpots[0] != "untested error path" {
		t.Fatalf("finding blind_spots = %#v", finding.BlindSpots)
	}

	// Verify normalization preserves new fields.
	normalized := normalizeFinding(finding)
	if normalized.TriggerCondition != "pointer used without nil check on line 12" {
		t.Fatalf("normalized trigger_condition = %q", normalized.TriggerCondition)
	}
	if normalized.Impact != "runtime panic in production if input is nil" {
		t.Fatalf("normalized impact = %q", normalized.Impact)
	}
	if !normalized.IntroducedByThisChange {
		t.Fatal("normalized introduced_by_this_change = false, want true")
	}
	if len(normalized.BlindSpots) != 1 || normalized.BlindSpots[0] != "untested error path" {
		t.Fatalf("normalized blind_spots = %#v", normalized.BlindSpots)
	}
}

func TestParseReviewResultWithMiniMaxAliasFields(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"rr-alias",
		"summary":"发现问题",
		"findings":[{
			"type":"code_defect",
			"title":"Loop upper bound is off by one",
			"severity":"high",
			"confidence":0.95,
			"evidence":"line 23 uses <= and can index past the end of the slice",
			"trigger_condition":"calculateTotal([{amount:100}]) triggers items[1] access",
			"impact":"runtime panic",
			"introduced_by_this_change":true,
			"actionable_fix":"change <= to <",
			"file_path":"src/lib/paymentCalculator.ts",
			"line_start":23,
			"line_end":23
		}]
	}`

	result, stage, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if stage != "direct" {
		t.Fatalf("stage = %q, want direct", stage)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(result.Findings))
	}
	finding := result.Findings[0]
	if finding.Category != "code_defect" {
		t.Fatalf("category = %q, want code_defect", finding.Category)
	}
	if finding.Path != "src/lib/paymentCalculator.ts" {
		t.Fatalf("path = %q, want src/lib/paymentCalculator.ts", finding.Path)
	}
	if finding.NewLine == nil || *finding.NewLine != 23 {
		t.Fatalf("new_line = %#v, want 23", finding.NewLine)
	}
	if len(finding.Evidence) != 1 || finding.Evidence[0] != "line 23 uses <= and can index past the end of the slice" {
		t.Fatalf("evidence = %#v", finding.Evidence)
	}
	if finding.SuggestedPatch != "change <= to <" {
		t.Fatalf("suggested_patch = %q, want alias-mapped actionable_fix", finding.SuggestedPatch)
	}
}

func TestParseReviewResultRejectsFractionalAliasLines(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"rr-fractional-line",
		"summary":"发现问题",
		"findings":[{
			"type":"code_defect",
			"title":"Fractional line",
			"severity":"high",
			"confidence":0.95,
			"body":"body",
			"file_path":"src/lib/paymentCalculator.ts",
			"line_start":23.5
		}]
	}`

	_, _, err := ParseReviewResult(raw)
	if err == nil {
		t.Fatal("expected parse error for fractional line number")
	}
}

func TestParseReviewResultRejectsOutOfRangeAliasLines(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"rr-big-line",
		"summary":"发现问题",
		"findings":[{
			"type":"code_defect",
			"title":"Huge line",
			"severity":"high",
			"confidence":0.95,
			"body":"body",
			"file_path":"src/lib/paymentCalculator.ts",
			"line_start":9999999999
		}]
	}`

	_, _, err := ParseReviewResult(raw)
	if err == nil {
		t.Fatal("expected parse error for out-of-range line number")
	}
}

func TestParseReviewResultDefaultsMissingCategory(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"rr-missing-category",
		"summary":"发现问题",
		"findings":[{
			"title":"Loop upper bound is off by one",
			"description":"calculateTotal can access past the end of the slice",
			"severity":"high",
			"confidence":0.95,
			"path":"src/lib/paymentCalculator.ts",
			"line_start":23,
			"evidence":"line 23 uses <= and can index past the end of the slice",
			"trigger_condition":"calculateTotal([{amount:100}]) triggers items[1] access",
			"impact":"runtime panic",
			"introduced_by_this_change":true
		}]
	}`

	result, _, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(result.Findings))
	}
	if result.Findings[0].Category != "code_defect" {
		t.Fatalf("category = %q, want code_defect default", result.Findings[0].Category)
	}
}

func TestParseReviewResultNewFieldsOptional(t *testing.T) {
	// Verify that existing JSON without the new fields still parses correctly.
	raw := `{"schema_version":"1.0","review_run_id":"rr-3","summary":"ok","findings":[{"category":"bug","severity":"high","confidence":0.91,"title":"Issue","body_markdown":"body","path":"main.go","anchor_kind":"new","new_line":5}]}`
	result, _, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	finding := result.Findings[0]
	if finding.TriggerCondition != "" {
		t.Fatalf("trigger_condition = %q, want empty", finding.TriggerCondition)
	}
	if finding.Impact != "" {
		t.Fatalf("impact = %q, want empty", finding.Impact)
	}
	if finding.IntroducedByThisChange {
		t.Fatal("introduced_by_this_change = true, want false")
	}
	if len(finding.BlindSpots) != 0 {
		t.Fatalf("blind_spots = %#v, want empty", finding.BlindSpots)
	}
	if len(result.BlindSpots) != 0 {
		t.Fatalf("result blind_spots = %#v, want empty", result.BlindSpots)
	}
}

func TestParserFallbackChain(t *testing.T) {
	t.Run("marker extraction", func(t *testing.T) {
		raw := "Here is the result:\n```json\n{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-1\",\"summary\":\"ok\",\"findings\":[]}\n```"
		_, stage, err := ParseReviewResult(raw)
		if err != nil {
			t.Fatalf("ParseReviewResult: %v", err)
		}
		if stage != "marker_extraction" {
			t.Fatalf("stage = %q, want marker_extraction", stage)
		}
	})
	t.Run("tolerant repair", func(t *testing.T) {
		raw := "{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-1\",\"summary\":\"ok\",\"findings\":[],}"
		_, stage, err := ParseReviewResult(raw)
		if err != nil {
			t.Fatalf("ParseReviewResult: %v", err)
		}
		if stage != "tolerant_repair" {
			t.Fatalf("stage = %q, want tolerant_repair", stage)
		}
	})
	t.Run("parser error", func(t *testing.T) {
		_, _, err := ParseReviewResult("definitely not json")
		if err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestProviderTimeoutRetry(t *testing.T) {
	transport := &captureTransport{errSequence: []error{timeoutError{}, timeoutError{}, timeoutError{}}}
	provider, err := NewMiniMaxProvider(ProviderConfig{BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "secret-token", Model: "MiniMax-M2.5", TimeoutRetries: 3, HTTPClient: &http.Client{Transport: transport}, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}
	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if transport.calls == 0 {
		t.Fatal("expected provider request attempts")
	}
}

func TestSecondaryProviderFallback(t *testing.T) {
	primary := &fakeProvider{err: scheduler.NewRetryableError("provider_request_failed", errors.New("upstream status 503"))}
	secondary := &fakeProvider{response: ProviderResponse{Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: "123", Summary: "ok", Findings: nil, Status: "completed"}, Model: "secondary", ResponsePayload: map[string]any{"provider": "secondary"}}}
	provider := NewFallbackProvider(slog.New(slog.NewTextHandler(io.Discard, nil)), primary, "primary-route", secondary, "secondary-route")

	response, err := provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if response.Model != "secondary" {
		t.Fatalf("response model = %q, want secondary", response.Model)
	}
	if response.ResponsePayload["fallback_from_provider_route"] != "primary-route" {
		t.Fatalf("fallback_from_provider_route = %#v, want primary-route", response.ResponsePayload["fallback_from_provider_route"])
	}
	if response.ResponsePayload["provider_route"] != "secondary-route" {
		t.Fatalf("provider_route = %#v, want secondary-route", response.ResponsePayload["provider_route"])
	}
	if !strings.Contains(response.FallbackStage, "secondary_provider") {
		t.Fatalf("fallback stage = %q, want secondary provider marker", response.FallbackStage)
	}
}

func TestProviderRouteSelection(t *testing.T) {
	loader := &fakeRulesLoader{result: rules.LoadResult{EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "project-route"}, Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	result, err := loader.Load(context.Background(), rules.LoadInput{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.EffectivePolicy.ProviderRoute != "project-route" {
		t.Fatalf("provider route = %q, want project-route", result.EffectivePolicy.ProviderRoute)
	}
}

func TestProcessRunUsesDynamicSystemPrompt(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{
			GitLabID:  11,
			IID:       7,
			ProjectID: 101,
			Title:     "Title",
			Author: struct {
				Username string `json:"username"`
			}{Username: "alice"},
			DiffRefs:     &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"},
			HeadSHA:      "head",
			WebURL:       "https://gitlab.example.com/group/proj/-/merge_requests/7",
			State:        "opened",
			SourceBranch: "feature",
			TargetBranch: "main",
		},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		SystemPrompt:    "dynamic system prompt: 输出语言 zh-CN",
		EffectivePolicy: rules.EffectivePolicy{OutputLanguage: "zh-CN"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"},
	}}
	provider := &fakeDynamicPromptProvider{fakeProvider: fakeProvider{response: ProviderResponse{
		Result: ReviewResult{
			SchemaVersion: "1.0",
			ReviewRunID:   fmt.Sprintf("%d", runID),
			Summary:       "摘要",
			Status:        "completed",
			Findings: []ReviewFinding{{
				Category:     "bug",
				Severity:     "high",
				Confidence:   0.9,
				Title:        "问题",
				BodyMarkdown: "内容",
				Path:         "main.go",
				AnchorKind:   "new",
			}},
		},
		Model: "MiniMax-M2.5",
	}}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	if _, err := processor.ProcessRun(ctx, run); err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if provider.systemPrompt != "dynamic system prompt: 输出语言 zh-CN" {
		t.Fatalf("system prompt = %q, want dynamic prompt from rules loader", provider.systemPrompt)
	}
}

func TestLLMRateLimiting(t *testing.T) {
	var slept []time.Duration
	current := time.Unix(0, 0)
	limiter := NewInMemoryRateLimiter(RateLimitConfig{Requests: 1, Window: time.Second}, func() time.Time { return current }, func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		current = current.Add(delay)
		return nil
	})
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"submit_review","input":{"schema_version":"1.0","review_run_id":"123","summary":"ok","findings":[]}}],"usage":{"output_tokens":1}}`}
	provider, err := NewMiniMaxProvider(ProviderConfig{BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "secret-token", Model: "MiniMax-M2.5", RouteName: "project-route", RateLimiter: limiter, HTTPClient: &http.Client{Transport: transport}, Now: func() time.Time { return current }})
	if err != nil {
		t.Fatalf("NewMiniMaxProvider: %v", err)
	}
	request := ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"}
	if _, err := provider.Review(context.Background(), request); err != nil {
		t.Fatalf("first Review: %v", err)
	}
	if _, err := provider.Review(context.Background(), request); err != nil {
		t.Fatalf("second Review: %v", err)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("sleep durations = %#v, want [1s]", slept)
	}
}

func TestRedactedLogging(t *testing.T) {
	payload := map[string]any{"api_key": "secret", "apiKey": "secret-two", "x-api-key": "secret-three", "Authorization": "Bearer abc", "messages": []any{map[string]any{"content": "very long prompt body"}}, "diff": stringsRepeat("x", 300)}
	redacted := redactPayload(payload)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"secret", "secret-two", "secret-three", "Bearer abc", "very long prompt body"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("redacted payload leaked %q: %s", forbidden, text)
		}
	}
	if !bytes.Contains(data, []byte("[REDACTED]")) {
		t.Fatalf("expected redaction marker: %s", text)
	}
	if !bytes.Contains(data, []byte("[OMITTED]")) {
		t.Fatalf("expected omission marker: %s", text)
	}
}

func TestRedactErrorNilSafe(t *testing.T) {
	if got := redactError(nil); got != nil {
		t.Fatalf("redactError(nil) = %#v, want nil", got)
	}
}

func TestWorkerExecutesRealProcessor(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	instanceID, projectID, mrID, runID := seedRun(t, ctx, q)
	_ = instanceID
	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title", Author: struct {
		Username string "json:\"username\""
	}{Username: "alice"}, DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}}, Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"}, Diffs: []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}}}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"}}}
	provider := &fakeProvider{response: ProviderResponse{Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{{Category: "bug", Severity: "high", Confidence: 0.9, Title: "Issue", BodyMarkdown: "body", Path: "main.go", AnchorKind: "new"}}}, Model: "MiniMax-M2.5", Tokens: 77, Latency: 25 * time.Millisecond, ResponsePayload: map[string]any{"token": "secret", "content": "prompt body"}}}
	registry := metrics2.NewRegistry()
	tracer := tracing.NewRecorder()
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB)).WithMetrics(registry).WithTracer(tracer)
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome status = %q, want requested_changes", outcome.Status)
	}
	if outcome.ProviderLatencyMs != 25 {
		t.Fatalf("outcome provider latency = %d, want 25", outcome.ProviderLatencyMs)
	}
	if outcome.ProviderTokensTotal != 77 {
		t.Fatalf("outcome provider tokens = %d, want 77", outcome.ProviderTokensTotal)
	}
	findingRows, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(findingRows) != 1 {
		t.Fatalf("findings = %d, want 1", len(findingRows))
	}
	updatedRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if updatedRun.Status != "requested_changes" {
		t.Fatalf("status = %q, want requested_changes", updatedRun.Status)
	}
	audits, err := q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	if len(audits) == 0 {
		t.Fatal("expected provider audit log")
	}
	var providerCallDetail map[string]any
	foundProviderCall := false
	for _, audit := range audits {
		if audit.Action != "provider_called" {
			continue
		}
		if err := json.Unmarshal(audit.Detail, &providerCallDetail); err != nil {
			t.Fatalf("unmarshal provider_called detail: %v", err)
		}
		foundProviderCall = true
		break
	}
	if !foundProviderCall {
		t.Fatal("expected provider_called audit log")
	}
	request, ok := providerCallDetail["request"].(map[string]any)
	if !ok {
		t.Fatalf("provider_called request = %#v, want object", providerCallDetail["request"])
	}
	if messages, ok := request["messages"].([]any); ok && len(messages) > 0 {
		firstMessage, ok := messages[0].(map[string]any)
		if !ok {
			t.Fatalf("provider_called first message = %#v, want object", messages[0])
		}
		if firstMessage["content"] == "[OMITTED]" {
			t.Fatalf("provider_called message content should be preserved: %#v", firstMessage)
		}
	} else {
		if request["content"] != "prompt body" {
			t.Fatalf("provider_called request content = %#v, want prompt body", request["content"])
		}
	}
	response, ok := providerCallDetail["response"].(map[string]any)
	if !ok {
		t.Fatalf("provider_called response = %#v, want object", providerCallDetail["response"])
	}
	if response["content"] != "prompt body" {
		t.Fatalf("provider_called response content = %#v, want prompt body", response["content"])
	}
	if projectID == 0 {
		t.Fatal("expected seeded project")
	}
	if got := registry.CounterValue("provider_tokens_total", nil); got != 77 {
		t.Fatalf("provider token metric = %d, want 77", got)
	}
	if spans := tracer.Spans(); len(spans) == 0 {
		t.Fatal("expected trace spans to be recorded")
	}
}

func TestProviderFailureAuditStoresFullRequestAndRawResponse(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	payload := map[string]any{
		"model": "MiniMax-M2.5",
		"messages": []map[string]any{{
			"role":    "user",
			"content": "full request payload",
		}},
	}
	rawResponse := "full raw response body"
	logger := NewDBAuditLogger(sqlDB)

	if err := logger.LogProviderFailure(ctx, run, payload, &providerParseError{cause: fmt.Errorf("llm: unable to parse provider response"), rawResponse: rawResponse, latency: 5 * time.Second, tokens: 321, model: "MiniMax-M2.5"}); err != nil {
		t.Fatalf("LogProviderFailure: %v", err)
	}

	audits, err := q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	var detail map[string]any
	found := false
	for _, audit := range audits {
		if audit.Action != "provider_failed" {
			continue
		}
		if err := json.Unmarshal(audit.Detail, &detail); err != nil {
			t.Fatalf("unmarshal provider_failed detail: %v", err)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("expected provider_failed audit log")
	}
	request, ok := detail["request"].(map[string]any)
	if !ok {
		t.Fatalf("provider_failed request = %#v, want object", detail["request"])
	}
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("provider_failed messages = %#v, want non-empty array", request["messages"])
	}
	firstMessage, ok := messages[0].(map[string]any)
	if !ok || firstMessage["content"] != "full request payload" {
		t.Fatalf("provider_failed first message = %#v, want full request payload", messages[0])
	}
	response, ok := detail["response"].(map[string]any)
	if !ok {
		t.Fatalf("provider_failed response = %#v, want object", detail["response"])
	}
	if response["text"] != rawResponse {
		t.Fatalf("provider_failed response text = %#v, want %q", response["text"], rawResponse)
	}
	if detail["provider_latency_ms"] != float64(5000) {
		t.Fatalf("provider_failed latency = %#v, want 5000", detail["provider_latency_ms"])
	}
	if detail["provider_tokens_total"] != float64(321) {
		t.Fatalf("provider_failed tokens = %#v, want 321", detail["provider_tokens_total"])
	}
	if detail["provider_model"] != "MiniMax-M2.5" {
		t.Fatalf("provider_failed model = %#v, want MiniMax-M2.5", detail["provider_model"])
	}
}

func TestWorkerThreadsPerPathReviewIntoReviewRequest(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title", Author: struct {
		Username string "json:\"username\""
	}{Username: "alice"}, DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}}, Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"}, Diffs: []gitlab.MergeRequestDiff{{OldPath: "src/auth/login.go", NewPath: "src/auth/login.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}, {OldPath: "pkg/util.go", NewPath: "pkg/util.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}, {OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}}}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ReviewMarkdown: "# Root review\n", DirectoryReviews: map[string]string{"src/auth": "# Auth review\n", "pkg": "# Pkg review\n"}, RulesDigest: "digest"}}}
	provider := &fakeProvider{response: ProviderResponse{Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: nil}, Model: "MiniMax-M2.5", ResponsePayload: map[string]any{}}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-path-review", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if _, err := processor.ProcessRun(ctx, run); err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}

	if provider.request.Rules.ReviewForPath("src/auth/login.go") != "# Auth review\n" {
		t.Fatalf("rules ReviewForPath(auth) = %q, want auth review", provider.request.Rules.ReviewForPath("src/auth/login.go"))
	}

	gotReviews := map[string]string{}
	for _, change := range provider.request.Changes {
		gotReviews[change.Path] = change.Review
	}
	if gotReviews["src/auth/login.go"] != "# Auth review\n" {
		t.Fatalf("auth change review = %q, want auth review", gotReviews["src/auth/login.go"])
	}
	if gotReviews["pkg/util.go"] != "# Pkg review\n" {
		t.Fatalf("pkg change review = %q, want pkg review", gotReviews["pkg/util.go"])
	}
	if gotReviews["main.go"] != "# Root review\n" {
		t.Fatalf("root change review = %q, want root review", gotReviews["main.go"])
	}
	if !reflect.DeepEqual(rulesLoader.inputs[0].ChangedPaths, []string{"src/auth/login.go", "pkg/util.go", "main.go"}) {
		t.Fatalf("loader changed paths = %#v, want all diff paths", rulesLoader.inputs[0].ChangedPaths)
	}
}

func TestDegradationSummaryNote(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)
	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title", Author: struct {
		Username string "json:\"username\""
	}{Username: "alice"}, DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}}, Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"}, Diffs: []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}, {OldPath: "other.go", NewPath: "other.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}}}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"}}}
	project, err := q.GetProject(ctx, 1)
	if err == nil {
		_, _ = q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{ProjectID: project.ID, ConfidenceThreshold: 0.1, SeverityThreshold: "low", IncludePaths: json.RawMessage("[]"), ExcludePaths: json.RawMessage("[]"), Extra: json.RawMessage(`{"review":{"max_files":1}}`)})
	}
	provider := &fakeProvider{}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
	actions, err := q.ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(actions) != 1 || actions[0].ActionType != "summary_note" {
		t.Fatalf("comment actions = %#v, want one summary_note", actions)
	}
}

func TestReviewedCleanPathBecomesFixed(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:reviewed-clean"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("GetReviewRun new: %v", err)
	}

	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: nil}, map[string]struct{}{"src/service/foo.go": {}}, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}

	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateFixed {
		t.Fatalf("state = %q, want fixed", findings[0].State)
	}
}

func TestReviewedCleanPathBecomesFixedFromNewFinding(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := persistFindings(ctx, q, baseRun, mr, ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   fmt.Sprintf("%d", runID),
		Summary:       "summary",
		Status:        "completed",
		Findings:      []ReviewFinding{sameRunFinding(12)},
	}, nil, nil); err != nil {
		t.Fatalf("persistFindings base: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:reviewed-clean-new"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("GetReviewRun new: %v", err)
	}

	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   fmt.Sprintf("%d", newRunID),
		Summary:       "summary",
		Status:        "completed",
		Findings:      nil,
	}, map[string]struct{}{"src/service/foo.go": {}}, nil); err != nil {
		t.Fatalf("persistFindings clean: %v", err)
	}

	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateFixed {
		t.Fatalf("state = %q, want fixed", findings[0].State)
	}
}

func TestReviewedScopeFromAssemblyIncludesReviewedCleanPaths(t *testing.T) {
	assembled := ctxpkg.AssemblyResult{Request: ctxpkg.ReviewRequest{Changes: []ctxpkg.Change{{Path: "src/service/foo.go", Status: "modified"}, {Path: "src/service/bar.go", Status: "deleted"}}}}

	reviewedPaths, deletedPaths := reviewedScopeFromAssembly(assembled)

	if _, ok := reviewedPaths["src/service/foo.go"]; !ok {
		t.Fatal("expected modified path to be marked reviewed even when no findings survive")
	}
	if _, ok := reviewedPaths["src/service/bar.go"]; !ok {
		t.Fatal("expected deleted path to be marked reviewed")
	}
	if _, ok := deletedPaths["src/service/bar.go"]; !ok {
		t.Fatal("expected deleted path to be marked deleted")
	}
}

func TestParserErrorStructuredResult(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)
	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title", Author: struct {
		Username string "json:\"username\""
	}{Username: "alice"}, DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}}, Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"}, Diffs: []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}}}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"}}}
	provider := &fakeProvider{err: scheduler.NewTerminalError("parser_error", &providerParseError{
		cause:       errors.New("unparseable provider output"),
		rawResponse: "not-json-response",
		latency:     7 * time.Second,
		tokens:      456,
		model:       "MiniMax-M2.5",
	})}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "parser_error" {
		t.Fatalf("outcome status = %q, want parser_error", outcome.Status)
	}
	if outcome.ProviderLatencyMs != 7000 {
		t.Fatalf("outcome provider_latency_ms = %d, want 7000", outcome.ProviderLatencyMs)
	}
	if outcome.ProviderTokensTotal != 456 {
		t.Fatalf("outcome provider_tokens_total = %d, want 456", outcome.ProviderTokensTotal)
	}
	updatedRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if updatedRun.Status != "parser_error" {
		t.Fatalf("status = %q, want parser_error", updatedRun.Status)
	}
	if err := q.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: outcome.Status, ErrorCode: "", ErrorDetail: sql.NullString{}, ID: runID}); err != nil {
		t.Fatalf("UpdateReviewRunStatus: %v", err)
	}
	updatedRun, err = q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if updatedRun.Status != "parser_error" {
		t.Fatalf("status = %q, want parser_error", updatedRun.Status)
	}
	actions, err := q.ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("comment actions = %d, want 1", len(actions))
	}
	if actions[0].ActionType != "summary_note" {
		t.Fatalf("action type = %q, want summary_note", actions[0].ActionType)
	}
	if actions[0].Status != "pending" {
		t.Fatalf("action status = %q, want pending", actions[0].Status)
	}
	findings, err := q.ListActiveFindingsByMR(ctx, run.MergeRequestID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(findings))
	}
}

func TestSuccessfulRunPersistsProviderMetrics(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		return scheduler.ProcessOutcome{Status: "completed", ProviderLatencyMs: 37, ProviderTokensTotal: 1234}, nil
	})
	svc := scheduler.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, processor, scheduler.WithWorkerID("worker-metrics"))
	processed, err := svc.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status = %q, want completed", run.Status)
	}
	if run.ProviderLatencyMs != 37 {
		t.Fatalf("provider_latency_ms = %d, want 37", run.ProviderLatencyMs)
	}
	if run.ProviderTokensTotal != 1234 {
		t.Fatalf("provider_tokens_total = %d, want 1234", run.ProviderTokensTotal)
	}
	findings, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(findings))
	}
}

func TestNormalizeFinding(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)

	oldLine := int32(7)
	newLine := int32(9)
	result := ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   fmt.Sprintf("%d", runID),
		Summary:       "summary",
		Status:        "completed",
		Findings: []ReviewFinding{{
			Category:       "bug",
			Severity:       "high",
			Confidence:     0.91,
			Title:          "Nil dereference",
			BodyMarkdown:   "Dereference may panic.",
			Path:           "src/service/foo.go",
			AnchorKind:     "new_line",
			OldLine:        &oldLine,
			NewLine:        &newLine,
			AnchorSnippet:  "return *ptr",
			Evidence:       []string{"ptr may be nil", "guard is missing"},
			SuggestedPatch: "if ptr == nil { return 0 }",
			CanonicalKey:   "nil-deref:foo-service",
			Symbol:         "(*Service).DoWork",
		}},
	}

	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := persistFindings(ctx, q, run, mr, result, nil, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}

	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}

	got := findings[0]
	if got.Category != "bug" || got.Severity != "high" || got.Confidence != 0.91 {
		t.Fatalf("unexpected classification fields: %+v", got)
	}
	if got.Title != "Nil dereference" || got.BodyMarkdown.String != "Dereference may panic." {
		t.Fatalf("unexpected title/body: %+v", got)
	}
	if got.Path != "src/service/foo.go" || got.AnchorKind != "new_line" {
		t.Fatalf("unexpected path/anchor_kind: %+v", got)
	}
	if !got.OldLine.Valid || got.OldLine.Int32 != oldLine || !got.NewLine.Valid || got.NewLine.Int32 != newLine {
		t.Fatalf("unexpected line anchors: old=%+v new=%+v", got.OldLine, got.NewLine)
	}
	if got.AnchorSnippet.String != "return *ptr" {
		t.Fatalf("anchor_snippet = %q, want %q", got.AnchorSnippet.String, "return *ptr")
	}
	if got.Evidence.String != "ptr may be nil\nguard is missing" {
		t.Fatalf("evidence = %q", got.Evidence.String)
	}
	if got.SuggestedPatch.String != "if ptr == nil { return 0 }" {
		t.Fatalf("suggested_patch = %q", got.SuggestedPatch.String)
	}
	if got.CanonicalKey != "nil-deref:foo-service" {
		t.Fatalf("canonical_key = %q", got.CanonicalKey)
	}
	if got.AnchorFingerprint == "" || got.SemanticFingerprint == "" {
		t.Fatalf("expected fingerprints to be populated: %+v", got)
	}
	if got.State != "new" {
		t.Fatalf("state = %q, want new", got.State)
	}

	wantAnchor := computeAnchorFingerprint(normalizedFinding{
		Path:          "src/service/foo.go",
		AnchorKind:    "new_line",
		AnchorSnippet: "return *ptr",
		Category:      "bug",
		CanonicalKey:  "nil-deref:foo-service",
	})
	wantSemantic := computeSemanticFingerprint(normalizedFinding{
		Path:         "src/service/foo.go",
		Category:     "bug",
		CanonicalKey: "nil-deref:foo-service",
		Symbol:       "(*Service).DoWork",
	})
	if got.AnchorFingerprint != wantAnchor {
		t.Fatalf("anchor_fingerprint = %q, want %q", got.AnchorFingerprint, wantAnchor)
	}
	if got.SemanticFingerprint != wantSemantic {
		t.Fatalf("semantic_fingerprint = %q, want %q", got.SemanticFingerprint, wantSemantic)
	}
	if got.MergeRequestID != mrID {
		t.Fatalf("merge_request_id = %d, want %d", got.MergeRequestID, mrID)
	}
	if got.ReviewRunID != runID {
		t.Fatalf("review_run_id = %d, want %d", got.ReviewRunID, runID)
	}
}

func TestAnchorFingerprintDeterministic(t *testing.T) {
	base := normalizedFinding{
		Path:          "src/foo.go",
		AnchorKind:    "new_line",
		AnchorSnippet: "if err != nil {",
		Category:      "bug",
		CanonicalKey:  "missing-error-context",
	}
	if got, want := computeAnchorFingerprint(base), computeAnchorFingerprint(base); got != want {
		t.Fatalf("anchor fingerprint not deterministic: %q != %q", got, want)
	}
	changed := base
	changed.AnchorSnippet = "if err != nil && retry {"
	if computeAnchorFingerprint(base) == computeAnchorFingerprint(changed) {
		t.Fatal("expected different anchor fingerprint for changed snippet")
	}
}

func TestCanonicalizeLegacyAnchorKinds(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty stays empty", in: "", want: ""},
		{name: "new stays new line", in: "new", want: "new_line"},
		{name: "new line stays canonical", in: "new_line", want: "new_line"},
		{name: "added maps to new line", in: "added", want: "new_line"},
		{name: "old stays old line", in: "old", want: "old_line"},
		{name: "old line stays canonical", in: "old_line", want: "old_line"},
		{name: "deleted maps to old line", in: "deleted", want: "old_line"},
		{name: "context stays context line", in: "context", want: "context_line"},
		{name: "context line stays canonical", in: "context_line", want: "context_line"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAnchorKind(tt.in); got != tt.want {
				t.Fatalf("normalizeAnchorKind(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRejectEmptyAnchorKind(t *testing.T) {
	if got := normalizeAnchorKind(""); got != "" {
		t.Fatalf("normalizeAnchorKind(empty) = %q, want empty", got)
	}

	normalized := normalizeFinding(ReviewFinding{
		Category:      "bug",
		Severity:      "high",
		Title:         "Missing anchor kind",
		BodyMarkdown:  "body",
		Path:          "pkg/foo.go",
		AnchorKind:    " ",
		AnchorSnippet: "return *ptr",
		CanonicalKey:  "missing-anchor-kind",
	})
	if normalized.AnchorKind != "" {
		t.Fatalf("normalized anchor kind = %q, want empty", normalized.AnchorKind)
	}
	if normalized.NewLine.Valid || normalized.OldLine.Valid {
		t.Fatalf("unexpected inferred lines: old=%+v new=%+v", normalized.OldLine, normalized.NewLine)
	}
}

func TestNormalizeFindingCanonicalizesLegacyAnchorLabels(t *testing.T) {
	legacyOldLine := int32(11)
	currentOldLine := int32(11)

	legacy := normalizeFinding(ReviewFinding{
		Category:      "bug",
		Severity:      "high",
		Title:         "Legacy anchor",
		BodyMarkdown:  "body",
		Path:          "pkg/foo.go",
		AnchorKind:    "deleted",
		OldLine:       &legacyOldLine,
		AnchorSnippet: "removed line",
		CanonicalKey:  "legacy-anchor",
	})
	current := normalizeFinding(ReviewFinding{
		Category:      "bug",
		Severity:      "high",
		Title:         "Current anchor",
		BodyMarkdown:  "body",
		Path:          "pkg/foo.go",
		AnchorKind:    "old_line",
		OldLine:       &currentOldLine,
		AnchorSnippet: "removed line",
		CanonicalKey:  "legacy-anchor",
	})

	if legacy.AnchorKind != "old_line" {
		t.Fatalf("legacy anchor kind = %q, want old_line", legacy.AnchorKind)
	}
	if current.AnchorKind != "old_line" {
		t.Fatalf("current anchor kind = %q, want old_line", current.AnchorKind)
	}
	if computeAnchorFingerprint(legacy) != computeAnchorFingerprint(current) {
		t.Fatal("expected canonicalized legacy/current anchors to share anchor fingerprint")
	}
}

func TestPersistFindingsCanonicalizesLegacyAnchorLabels(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)

	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}

	newLine := int32(27)
	legacy := ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   fmt.Sprintf("%d", runID),
		Summary:       "summary",
		Status:        "completed",
		Findings: []ReviewFinding{{
			Category:      "bug",
			Severity:      "high",
			Confidence:    0.8,
			Title:         "Equivalent anchor vocabulary",
			BodyMarkdown:  "body",
			Path:          "pkg/service.go",
			AnchorKind:    "added",
			NewLine:       &newLine,
			AnchorSnippet: "if err != nil { return err }",
			CanonicalKey:  "equivalent-anchor-vocabulary",
		}},
	}
	if err := persistFindings(ctx, q, run, mr, legacy, nil, nil); err != nil {
		t.Fatalf("persistFindings legacy: %v", err)
	}

	legacyRows, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun legacy: %v", err)
	}
	if len(legacyRows) != 1 {
		t.Fatalf("legacy rows = %d, want 1", len(legacyRows))
	}
	if legacyRows[0].AnchorKind != "new_line" {
		t.Fatalf("legacy anchor kind persisted as %q, want new_line", legacyRows[0].AnchorKind)
	}
	legacyFingerprint := legacyRows[0].AnchorFingerprint

	secondRunResult, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, Status: "pending", TriggerType: "merge_request", HeadSha: "head-sha-2"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	secondRunID, err := secondRunResult.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	secondRun, err := q.GetReviewRun(ctx, secondRunID)
	if err != nil {
		t.Fatalf("GetReviewRun second run: %v", err)
	}

	current := ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   fmt.Sprintf("%d", secondRunID),
		Summary:       "summary",
		Status:        "completed",
		Findings: []ReviewFinding{{
			Category:      "bug",
			Severity:      "high",
			Confidence:    0.8,
			Title:         "Equivalent anchor vocabulary",
			BodyMarkdown:  "body",
			Path:          "pkg/service.go",
			AnchorKind:    "new_line",
			NewLine:       &newLine,
			AnchorSnippet: "if err != nil { return err }",
			CanonicalKey:  "equivalent-anchor-vocabulary",
		}},
	}
	if err := persistFindings(ctx, q, secondRun, mr, current, nil, nil); err != nil {
		t.Fatalf("persistFindings current: %v", err)
	}

	currentRows, err := q.ListFindingsByRun(ctx, secondRunID)
	if err != nil {
		t.Fatalf("ListFindingsByRun current: %v", err)
	}
	if len(currentRows) == 0 {
		activeRows, listErr := q.ListActiveFindingsByMR(ctx, mrID)
		if listErr != nil {
			t.Fatalf("ListActiveFindingsByMR: %v", listErr)
		}
		if len(activeRows) != 1 {
			t.Fatalf("active rows = %d, want 1", len(activeRows))
		}
		currentRows = activeRows
	}
	if currentRows[0].AnchorKind != "new_line" {
		t.Fatalf("current anchor kind persisted as %q, want new_line", currentRows[0].AnchorKind)
	}
	if currentRows[0].AnchorFingerprint != legacyFingerprint {
		t.Fatalf("anchor fingerprint = %q, want %q", currentRows[0].AnchorFingerprint, legacyFingerprint)
	}
	if currentRows[0].SemanticFingerprint != legacyRows[0].SemanticFingerprint {
		t.Fatalf("semantic fingerprint = %q, want %q", currentRows[0].SemanticFingerprint, legacyRows[0].SemanticFingerprint)
	}
}

func TestSemanticFingerprintDeterministic(t *testing.T) {
	base := normalizedFinding{
		Path:         "pkg/foo.go",
		Category:     "bug",
		CanonicalKey: "missing-nil-check",
		Symbol:       "(*Server).Handle",
	}
	withLineShift := base
	withLineShift.OldLine = sql.NullInt32{Int32: 10, Valid: true}
	withLineShift.NewLine = sql.NullInt32{Int32: 30, Valid: true}
	if got, want := computeSemanticFingerprint(base), computeSemanticFingerprint(withLineShift); got != want {
		t.Fatalf("semantic fingerprint changed across line shift: %q != %q", got, want)
	}
	changedSymbol := base
	changedSymbol.Symbol = "(*Server).Serve"
	if computeSemanticFingerprint(base) == computeSemanticFingerprint(changedSymbol) {
		t.Fatal("expected different semantic fingerprint for changed symbol")
	}
}

func TestCanonicalKeyFallback(t *testing.T) {
	base := ReviewFinding{Title: "Missing nil check", Path: "pkg/service.go", Category: "bug", AnchorKind: "new_line", AnchorSnippet: "return *ptr"}
	normalizedA := normalizeFinding(base)
	if normalizedA.CanonicalKey != "missing nil check::pkg/service.go" {
		t.Fatalf("canonical key fallback = %q", normalizedA.CanonicalKey)
	}
	normalizedB := normalizeFinding(ReviewFinding{Title: "Missing nil check", Path: "pkg/service.go", Category: "bug", AnchorKind: "new_line", AnchorSnippet: "return *ptr", NewLine: func() *int32 { v := int32(44); return &v }()})
	if normalizedA.CanonicalKey != normalizedB.CanonicalKey {
		t.Fatalf("fallback canonical key unstable: %q != %q", normalizedA.CanonicalKey, normalizedB.CanonicalKey)
	}
	if computeSemanticFingerprint(normalizedA) != computeSemanticFingerprint(normalizedB) {
		t.Fatal("semantic fingerprint should stay stable with fallback canonical key")
	}
	if computeAnchorFingerprint(normalizedA) != computeAnchorFingerprint(normalizedB) {
		t.Fatal("anchor fingerprint should stay stable with fallback canonical key when other inputs match")
	}
}

func TestSameHeadDedupe(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}

	result := ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{sameRunFinding(12), sameRunFinding(12)}}
	if err := persistFindings(ctx, q, run, mr, result, nil, nil); err != nil {
		t.Fatalf("first persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if err := q.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: findingStatePosted, ID: findings[0].ID}); err != nil {
		t.Fatalf("UpdateFindingState posted: %v", err)
	}
	if err := q.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: findingStateActive, ID: findings[0].ID}); err != nil {
		t.Fatalf("UpdateFindingState active: %v", err)
	}
	if err := persistFindings(ctx, q, run, mr, result, nil, nil); err != nil {
		t.Fatalf("second persistFindings: %v", err)
	}

	findings, err = q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].LastSeenRunID.Valid {
		t.Fatalf("last_seen_run_id = %+v, want invalid", findings[0].LastSeenRunID)
	}
	if findings[0].State != findingStateActive {
		t.Fatalf("state = %q, want active", findings[0].State)
	}
}

func TestNewHeadLastSeenUpdate(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("GetReviewRun new: %v", err)
	}
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{sameRunFinding(12)}}, nil, nil); err != nil {
		t.Fatalf("persistFindings new: %v", err)
	}

	active, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active findings = %d, want 1", len(active))
	}
	if !active[0].LastSeenRunID.Valid || active[0].LastSeenRunID.Int64 != newRunID {
		t.Fatalf("last_seen_run_id = %+v, want %d", active[0].LastSeenRunID, newRunID)
	}
}

func TestSemanticRelocation(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}
	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	relocated := sameRunFinding(30)
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{relocated}}, nil, nil); err != nil {
		t.Fatalf("persistFindings relocated: %v", err)
	}
	active, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active findings = %d, want 1", len(active))
	}
	if active[0].ReviewRunID != runID {
		t.Fatalf("active finding review_run_id = %d, want base run %d", active[0].ReviewRunID, runID)
	}
	got, err := q.GetReviewFinding(ctx, active[0].ID)
	if err != nil {
		t.Fatalf("GetReviewFinding: %v", err)
	}
	if !got.LastSeenRunID.Valid || got.LastSeenRunID.Int64 != newRunID {
		t.Fatalf("last_seen_run_id = %+v, want %d", got.LastSeenRunID, newRunID)
	}
	if got.State != findingStateActive {
		t.Fatalf("state = %q, want active", got.State)
	}
	if got.Path != relocated.Path {
		t.Fatalf("path = %q, want %q", got.Path, relocated.Path)
	}
	if got.AnchorKind != relocated.AnchorKind {
		t.Fatalf("anchor_kind = %q, want %q", got.AnchorKind, relocated.AnchorKind)
	}
	if !got.NewLine.Valid || got.NewLine.Int32 != 12 {
		t.Fatalf("new_line = %+v, want original line 12 until relocation line persistence is implemented", got.NewLine)
	}
	if got.OldLine.Valid {
		t.Fatalf("old_line = %+v, want invalid", got.OldLine)
	}
	if !got.AnchorSnippet.Valid || got.AnchorSnippet.String != relocated.AnchorSnippet {
		t.Fatalf("anchor_snippet = %+v, want %q", got.AnchorSnippet, relocated.AnchorSnippet)
	}
	wantAnchor := computeAnchorFingerprint(normalizeFinding(sameRunFinding(12)))
	if got.AnchorFingerprint != wantAnchor {
		t.Fatalf("anchor_fingerprint = %q, want original anchor %q", got.AnchorFingerprint, wantAnchor)
	}
	baseFindings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base: %v", err)
	}
	if len(baseFindings) != 1 {
		t.Fatalf("base run findings = %d, want 1", len(baseFindings))
	}
	if baseFindings[0].State != findingStateActive {
		t.Fatalf("base state = %q, want active", baseFindings[0].State)
	}
	if baseFindings[0].ID != got.ID {
		t.Fatalf("base finding id = %d, want relocated existing id %d", baseFindings[0].ID, got.ID)
	}
	if baseFindings[0].MatchedFindingID.Valid {
		t.Fatalf("base matched_finding_id = %+v, want invalid", baseFindings[0].MatchedFindingID)
	}
}

func TestRelocationSupersedes(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}
	baseFindings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base: %v", err)
	}
	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	relocated := sameRunFinding(30)
	relocated.AnchorSnippet = "different snippet"
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{relocated}}, nil, nil); err != nil {
		t.Fatalf("persistFindings relocated: %v", err)
	}
	newRunFindings, err := q.ListFindingsByRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("ListFindingsByRun new: %v", err)
	}
	if len(newRunFindings) != 0 {
		t.Fatalf("new run findings = %d, want 0 when semantic relocation keeps existing row active", len(newRunFindings))
	}
	oldFinding, err := q.GetReviewFinding(ctx, baseFindings[0].ID)
	if err != nil {
		t.Fatalf("GetReviewFinding old: %v", err)
	}
	if oldFinding.State != findingStateActive {
		t.Fatalf("old state = %q, want active", oldFinding.State)
	}
	if oldFinding.MatchedFindingID.Valid {
		t.Fatalf("matched_finding_id = %+v, want invalid", oldFinding.MatchedFindingID)
	}
	if !oldFinding.LastSeenRunID.Valid || oldFinding.LastSeenRunID.Int64 != newRunID {
		t.Fatalf("last_seen_run_id = %+v, want %d", oldFinding.LastSeenRunID, newRunID)
	}
}

func TestSameRunDuplicateCollapse(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	dupA := sameRunFinding(12)
	dupB := sameRunFinding(12)
	dupB.BodyMarkdown = "different wording"
	if err := persistFindings(ctx, q, run, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{dupA, dupB}}, nil, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		next    string
		wantOK  bool
	}{
		{name: "new to posted", current: findingStateNew, next: findingStatePosted, wantOK: true},
		{name: "new to fixed", current: findingStateNew, next: findingStateFixed, wantOK: true},
		{name: "new to stale", current: findingStateNew, next: findingStateStale, wantOK: true},
		{name: "new to ignored", current: findingStateNew, next: findingStateIgnored, wantOK: true},
		{name: "posted to active", current: findingStatePosted, next: findingStateActive, wantOK: true},
		{name: "posted to fixed", current: findingStatePosted, next: findingStateFixed, wantOK: true},
		{name: "posted to stale", current: findingStatePosted, next: findingStateStale, wantOK: true},
		{name: "posted to ignored", current: findingStatePosted, next: findingStateIgnored, wantOK: true},
		{name: "active to fixed", current: findingStateActive, next: findingStateFixed, wantOK: true},
		{name: "active to superseded", current: findingStateActive, next: findingStateSuperseded, wantOK: true},
		{name: "active to stale", current: findingStateActive, next: findingStateStale, wantOK: true},
		{name: "active to ignored", current: findingStateActive, next: findingStateIgnored, wantOK: true},
		{name: "fixed to active rejected", current: findingStateFixed, next: findingStateActive, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := nextFindingState(tc.current, tc.next)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("nextFindingState error: %v", err)
				}
				if !ok || got != tc.next {
					t.Fatalf("nextFindingState(%q, %q) = (%q, %v), want (%q, true)", tc.current, tc.next, got, ok, tc.next)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q -> %q", tc.current, tc.next)
			}
		})
	}
}

func TestMissingFindingFixed(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, run, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	remaining := sameRunFinding(30)
	remaining.Path = "src/service/foo.go"
	remaining.CanonicalKey = "nil-deref:foo-service:reviewed-remaining"
	remaining.Symbol = "(*Service).DoReviewedWork"
	remaining.AnchorSnippet = "return *reviewedPtr"
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{remaining}}, map[string]struct{}{normalizePath(remaining.Path): {}}, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateFixed {
		t.Fatalf("state = %q, want fixed", findings[0].State)
	}
}

func TestMissingFindingStale(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, run, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:stale"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	reviewedOtherPath := sameRunFinding(30)
	reviewedOtherPath.Path = "src/service/bar.go"
	reviewedOtherPath.CanonicalKey = "nil-deref:bar-service:stale-scope"
	reviewedOtherPath.Symbol = "(*Service).DoOtherWork"
	reviewedOtherPath.AnchorSnippet = "return *otherPtr"
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{reviewedOtherPath}}, map[string]struct{}{normalizePath(reviewedOtherPath.Path): {}}, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateStale {
		t.Fatalf("state = %q, want stale", findings[0].State)
	}
}

func TestMissingFindingNoReviewedScopeNoTransition(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, run, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:no-reviewed-scope"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: nil}, nil, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateActive {
		t.Fatalf("state = %q, want active", findings[0].State)
	}
}

func TestDeletedFileFixed(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if err := activateSingleFinding(ctx, q, run, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:deleted"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	deleted := sameRunFinding(0)
	deleted.Path = "src/service/foo.go"
	deleted.AnchorSnippet = "return *ptr"
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)
	deleted.AnchorKind = "deleted"
	deleted.CanonicalKey = "deleted:nil-deref:foo-service"
	deleted.NewLine = nil
	deleted.OldLine = func() *int32 { v := int32(12); return &v }()
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{deleted}}, map[string]struct{}{normalizePath(deleted.Path): {}}, map[string]struct{}{normalizePath(deleted.Path): {}}); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	newRunFindings, err := q.ListFindingsByRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("ListFindingsByRun new run: %v", err)
	}
	if len(newRunFindings) != 0 {
		t.Fatalf("new run findings = %d, want 0 because deleted anchors should only drive lifecycle", len(newRunFindings))
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if findings[0].State != findingStateFixed {
		t.Fatalf("state = %q, want fixed", findings[0].State)
	}
}

func TestReviewedCleanPathBecomesFixedAfterCarryForwardRerun(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	carryForwardRes, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:carry-forward-reviewed"})
	if err != nil {
		t.Fatalf("InsertReviewRun carry forward: %v", err)
	}
	carryForwardRunID, err := carryForwardRes.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId carry forward: %v", err)
	}
	carryForwardRun, err := q.GetReviewRun(ctx, carryForwardRunID)
	if err != nil {
		t.Fatalf("GetReviewRun carry forward: %v", err)
	}
	if err := persistFindings(ctx, q, carryForwardRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", carryForwardRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{sameRunFinding(12)}}, map[string]struct{}{normalizePath("src/service/foo.go"): {}}, nil); err != nil {
		t.Fatalf("persistFindings carry forward: %v", err)
	}

	baseFindings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after carry forward: %v", err)
	}
	if len(baseFindings) != 1 {
		t.Fatalf("base findings after carry forward = %d, want 1", len(baseFindings))
	}
	if !baseFindings[0].LastSeenRunID.Valid || baseFindings[0].LastSeenRunID.Int64 != carryForwardRunID {
		t.Fatalf("last_seen_run_id after carry forward = %+v, want %d", baseFindings[0].LastSeenRunID, carryForwardRunID)
	}
	if baseFindings[0].State != findingStateActive {
		t.Fatalf("state after carry forward = %q, want active", baseFindings[0].State)
	}

	cleanRes, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-3", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-3:webhook:clean-reviewed"})
	if err != nil {
		t.Fatalf("InsertReviewRun clean: %v", err)
	}
	cleanRunID, err := cleanRes.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId clean: %v", err)
	}
	cleanRun, err := q.GetReviewRun(ctx, cleanRunID)
	if err != nil {
		t.Fatalf("GetReviewRun clean: %v", err)
	}
	if err := persistFindings(ctx, q, cleanRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", cleanRunID), Summary: "summary", Status: "completed", Findings: nil}, map[string]struct{}{normalizePath("src/service/foo.go"): {}}, nil); err != nil {
		t.Fatalf("persistFindings clean: %v", err)
	}

	baseFindings, err = q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after clean: %v", err)
	}
	if baseFindings[0].State != findingStateFixed {
		t.Fatalf("state after clean rerun = %q, want fixed", baseFindings[0].State)
	}
	if !baseFindings[0].LastSeenRunID.Valid || baseFindings[0].LastSeenRunID.Int64 != carryForwardRunID {
		t.Fatalf("last_seen_run_id after clean rerun = %+v, want %d", baseFindings[0].LastSeenRunID, carryForwardRunID)
	}
}

func TestDeletedFileFixedAfterCarryForwardRerun(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	baseRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if err := activateSingleFinding(ctx, q, baseRun, mr, sameRunFinding(12)); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	carryForwardRes, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:carry-forward-deleted"})
	if err != nil {
		t.Fatalf("InsertReviewRun carry forward: %v", err)
	}
	carryForwardRunID, err := carryForwardRes.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId carry forward: %v", err)
	}
	carryForwardRun, err := q.GetReviewRun(ctx, carryForwardRunID)
	if err != nil {
		t.Fatalf("GetReviewRun carry forward: %v", err)
	}
	if err := persistFindings(ctx, q, carryForwardRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", carryForwardRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{sameRunFinding(12)}}, map[string]struct{}{normalizePath("src/service/foo.go"): {}}, nil); err != nil {
		t.Fatalf("persistFindings carry forward: %v", err)
	}

	baseFindings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after carry forward: %v", err)
	}
	if !baseFindings[0].LastSeenRunID.Valid || baseFindings[0].LastSeenRunID.Int64 != carryForwardRunID {
		t.Fatalf("last_seen_run_id after carry forward = %+v, want %d", baseFindings[0].LastSeenRunID, carryForwardRunID)
	}

	deletedRes, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-3", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-3:webhook:deleted-after-carry-forward"})
	if err != nil {
		t.Fatalf("InsertReviewRun deleted: %v", err)
	}
	deletedRunID, err := deletedRes.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId deleted: %v", err)
	}
	deletedRun, err := q.GetReviewRun(ctx, deletedRunID)
	if err != nil {
		t.Fatalf("GetReviewRun deleted: %v", err)
	}
	deleted := sameRunFinding(0)
	deleted.Path = "src/service/foo.go"
	deleted.AnchorSnippet = "return *ptr"
	deleted.AnchorKind = "deleted"
	deleted.CanonicalKey = "deleted:nil-deref:foo-service"
	deleted.NewLine = nil
	deleted.OldLine = func() *int32 { v := int32(12); return &v }()
	if err := persistFindings(ctx, q, deletedRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", deletedRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{deleted}}, map[string]struct{}{normalizePath(deleted.Path): {}}, map[string]struct{}{normalizePath(deleted.Path): {}}); err != nil {
		t.Fatalf("persistFindings deleted: %v", err)
	}

	baseFindings, err = q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after deleted: %v", err)
	}
	if baseFindings[0].State != findingStateFixed {
		t.Fatalf("state after deleted rerun = %q, want fixed", baseFindings[0].State)
	}
	if !baseFindings[0].LastSeenRunID.Valid || baseFindings[0].LastSeenRunID.Int64 != carryForwardRunID {
		t.Fatalf("last_seen_run_id after deleted rerun = %+v, want %d", baseFindings[0].LastSeenRunID, carryForwardRunID)
	}
}

func TestDeletedAnchorCanonicalizationTriggersDeletedLifecycle(t *testing.T) {
	deletedOldLine := int32(12)
	normalized := normalizeFinding(ReviewFinding{
		Category:      "bug",
		Severity:      "high",
		Title:         "Deleted file finding",
		BodyMarkdown:  "body",
		Path:          "src/service/foo.go",
		AnchorKind:    "deleted",
		OldLine:       &deletedOldLine,
		AnchorSnippet: "return *ptr",
		CanonicalKey:  "deleted:nil-deref:foo-service",
	})
	if normalized.AnchorKind != "old_line" {
		t.Fatalf("normalized anchor kind = %q, want old_line", normalized.AnchorKind)
	}
	if state := evaluateFindingState(normalized, findingThresholds{}); state != findingStateDeleted {
		t.Fatalf("evaluateFindingState() = %q, want %q", state, findingStateDeleted)
	}
	current := db.ReviewFinding{State: findingStateActive, Path: "src/service/foo.go", AnchorKind: "old_line"}
	next, ok, err := transitionMissingFinding(current, map[string]struct{}{"src/service/foo.go": {}}, map[string]struct{}{"src/service/foo.go": {}}, false)
	if err != nil {
		t.Fatalf("transitionMissingFinding: %v", err)
	}
	if !ok || next != findingStateFixed {
		t.Fatalf("transitionMissingFinding() = (%q, %v), want (%q, true)", next, ok, findingStateFixed)
	}
}

func TestMixedScopeMissingFindingStaysStale(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)

	baseFinding := sameRunFinding(12)
	if err := activateSingleFinding(ctx, q, run, mr, baseFinding); err != nil {
		t.Fatalf("activateSingleFinding base: %v", err)
	}
	baseRows, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after first insert: %v", err)
	}
	if len(baseRows) != 1 {
		t.Fatalf("base rows after first insert = %d, want 1", len(baseRows))
	}
	otherFinding := sameRunFinding(21)
	otherFinding.Path = "src/service/bar.go"
	otherFinding.CanonicalKey = "nil-deref:bar-service"
	otherFinding.Symbol = "(*Service).DoOtherWork"
	otherFinding.AnchorSnippet = "return *otherPtr"
	if err := persistFindings(ctx, q, run, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", run.ID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{otherFinding}}, nil, nil); err != nil {
		t.Fatalf("persistFindings other: %v", err)
	}
	baseRows, err = q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base after second insert: %v", err)
	}
	if len(baseRows) != 2 {
		t.Fatalf("base rows after second insert = %d, want 2", len(baseRows))
	}
	for _, finding := range baseRows {
		if finding.Path == otherFinding.Path {
			goto secondActiveReady
		}
	}
	t.Fatal("expected second active finding on reviewed file")

secondActiveReady:

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: run.ProjectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head-2", Status: "running", MaxRetries: 3, IdempotencyKey: "project:101:mr:7:head-2:webhook:mixed-scope"})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	newRunID, _ := res.LastInsertId()
	newRun, _ := q.GetReviewRun(ctx, newRunID)

	reviewedAbsentReplacement := sameRunFinding(30)
	reviewedAbsentReplacement.Path = otherFinding.Path
	reviewedAbsentReplacement.CanonicalKey = otherFinding.CanonicalKey
	reviewedAbsentReplacement.Symbol = otherFinding.Symbol
	reviewedAbsentReplacement.AnchorSnippet = otherFinding.AnchorSnippet
	if err := persistFindings(ctx, q, newRun, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{reviewedAbsentReplacement}}, map[string]struct{}{normalizePath(reviewedAbsentReplacement.Path): {}}, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}

	baseRunFindings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun base: %v", err)
	}
	statesByPath := map[string]string{}
	for _, finding := range baseRunFindings {
		statesByPath[finding.Path] = finding.State
	}
	if got := statesByPath[baseFinding.Path]; got != findingStateStale {
		t.Fatalf("state for %s = %q, want stale", baseFinding.Path, got)
	}
	if got := statesByPath[otherFinding.Path]; got != findingStateNew {
		t.Fatalf("state for %s = %q, want new", otherFinding.Path, got)
	}
}

func TestConfidenceThresholdFilter(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if _, err := q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{ProjectID: projectID, ConfidenceThreshold: 0.95, SeverityThreshold: "low", IncludePaths: json.RawMessage("[]"), ExcludePaths: json.RawMessage("[]"), Extra: json.RawMessage("{}")}); err != nil {
		t.Fatalf("InsertProjectPolicy: %v", err)
	}
	if err := persistFindings(ctx, q, run, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{sameRunFinding(12)}}, nil, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].State != findingStateFiltered {
		t.Fatalf("state = %q, want filtered", findings[0].State)
	}
}

func TestSeverityThresholdFilter(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, runID := seedRun(t, ctx, q)
	run, _ := q.GetReviewRun(ctx, runID)
	mr, _ := q.GetMergeRequest(ctx, mrID)
	if _, err := q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{ProjectID: projectID, ConfidenceThreshold: 0.10, SeverityThreshold: "high", IncludePaths: json.RawMessage("[]"), ExcludePaths: json.RawMessage("[]"), Extra: json.RawMessage("{}")}); err != nil {
		t.Fatalf("InsertProjectPolicy: %v", err)
	}
	lowSeverity := sameRunFinding(12)
	lowSeverity.Severity = "medium"
	if err := persistFindings(ctx, q, run, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{lowSeverity}}, nil, nil); err != nil {
		t.Fatalf("persistFindings: %v", err)
	}
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].State != findingStateFiltered {
		t.Fatalf("state = %q, want filtered", findings[0].State)
	}
}

func activateSingleFinding(ctx context.Context, q *db.Queries, run db.ReviewRun, mr db.MergeRequest, finding ReviewFinding) error {
	if err := persistFindings(ctx, q, run, mr, ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", run.ID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{finding}}, nil, nil); err != nil {
		return err
	}
	findings, err := q.ListFindingsByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(findings) != 1 {
		return fmt.Errorf("findings = %d, want 1", len(findings))
	}
	if err := q.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: findingStatePosted, ID: findings[0].ID}); err != nil {
		return err
	}
	return q.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: findingStateActive, ID: findings[0].ID})
}

func sameRunFinding(line int32) ReviewFinding {
	return ReviewFinding{
		Category:      "bug",
		Severity:      "high",
		Confidence:    0.91,
		Title:         "Nil dereference",
		BodyMarkdown:  "Dereference may panic.",
		Path:          "src/service/foo.go",
		AnchorKind:    "new_line",
		NewLine:       &line,
		AnchorSnippet: "return *ptr",
		Evidence:      []string{"ptr may be nil"},
		CanonicalKey:  "nil-deref:foo-service",
		Symbol:        "(*Service).DoWork",
	}
}

type captureTransport struct {
	body           bytes.Buffer
	header         http.Header
	responseBody   string
	responseBodies []string
	errSequence    []error
	calls          int
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	t.header = req.Header.Clone()
	_, _ = io.Copy(&t.body, req.Body)
	if len(t.errSequence) > 0 {
		err := t.errSequence[0]
		t.errSequence = t.errSequence[1:]
		return nil, err
	}
	responseBody := t.responseBody
	if len(t.responseBodies) > 0 {
		responseBody = t.responseBodies[0]
		t.responseBodies = t.responseBodies[1:]
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewBufferString(responseBody))}, nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type fakeGitLabReader struct{ snapshot gitlab.MergeRequestSnapshot }

func (f *fakeGitLabReader) GetMergeRequestSnapshot(context.Context, int64, int64) (gitlab.MergeRequestSnapshot, error) {
	return f.snapshot, nil
}

type fakeRulesLoader struct {
	result rules.LoadResult
	inputs []rules.LoadInput
}

func (f *fakeRulesLoader) Load(_ context.Context, input rules.LoadInput) (rules.LoadResult, error) {
	f.inputs = append(f.inputs, input)
	return f.result, nil
}

type fakeProvider struct {
	response ProviderResponse
	err      error
	request  ctxpkg.ReviewRequest
}

func (f *fakeProvider) Review(_ context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	f.request = request
	if f.err != nil {
		f.response.Latency = 13 * time.Millisecond
		f.response.Tokens = 21
		f.response.Model = "MiniMax-M2.5"
	}
	return f.response, f.err
}
func (f *fakeProvider) RequestPayload(ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"token": "secret", "content": "prompt body"}
}

type fakeDynamicPromptProvider struct {
	fakeProvider
	systemPrompt string
}

func (f *fakeDynamicPromptProvider) ReviewWithSystemPrompt(_ context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	f.request = request
	f.systemPrompt = systemPrompt
	if f.err != nil {
		f.response.Latency = 13 * time.Millisecond
		f.response.Tokens = 21
		f.response.Model = "MiniMax-M2.5"
	}
	return f.response, f.err
}

func (f *fakeDynamicPromptProvider) RequestPayloadWithSystemPrompt(ctxpkg.ReviewRequest, string) map[string]any {
	return map[string]any{"token": "secret", "content": "prompt body"}
}

func seedRun(t *testing.T, ctx context.Context, q *db.Queries) (int64, int64, int64, int64) {
	t.Helper()
	res, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{Url: "https://gitlab.example.com", Name: "GitLab"})
	if err != nil {
		t.Fatalf("UpsertGitlabInstance: %v", err)
	}
	instanceID, _ := res.LastInsertId()
	if instanceID == 0 {
		instance, err := q.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
		if err != nil {
			t.Fatalf("GetGitlabInstanceByURL: %v", err)
		}
		instanceID = instance.ID
	}
	res, err = q.UpsertProject(ctx, db.UpsertProjectParams{GitlabInstanceID: instanceID, GitlabProjectID: 101, PathWithNamespace: "group/project", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	projectID, _ := res.LastInsertId()
	if projectID == 0 {
		project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{GitlabInstanceID: instanceID, GitlabProjectID: 101})
		if err != nil {
			t.Fatalf("GetProjectByGitlabID: %v", err)
		}
		projectID = project.ID
	}
	res, err = q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{ProjectID: projectID, MrIid: 7, Title: "Title", SourceBranch: "feature", TargetBranch: "main", Author: "alice", State: "opened", IsDraft: false, HeadSha: "head", WebUrl: "https://gitlab.example.com/group/project/-/merge_requests/7"})
	if err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mrID, _ := res.LastInsertId()
	if mrID == 0 {
		mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{ProjectID: projectID, MrIid: 7})
		if err != nil {
			t.Fatalf("GetMergeRequestByProjectMR: %v", err)
		}
		mrID = mr.ID
	}
	res, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head", Status: "pending", MaxRetries: 3, IdempotencyKey: fmt.Sprintf("rr-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := res.LastInsertId()
	return instanceID, projectID, mrID, runID
}

func stringsRepeat(s string, count int) string { return strings.Repeat(s, count) }

func stringsContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func TestIsTimeoutError(t *testing.T) {
	if !isTimeoutError(timeoutError{}) {
		t.Fatal("expected timeoutError to be classified as timeout")
	}
	if isTimeoutError(errors.New("boom")) {
		t.Fatal("unexpected timeout classification")
	}
	var netErr net.Error = timeoutError{}
	if !isTimeoutError(netErr) {
		t.Fatal("expected net.Error timeout classification")
	}
}

// TestProviderRoutePolicySelectsRuntimeProvider proves that a ProviderRegistry
// resolves different providers for different route names, and that the
// Processor selects the correct provider based on EffectivePolicy.ProviderRoute.
func TestProviderRoutePolicySelectsRuntimeProvider(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}

	// Two providers that track which route was called.
	defaultCalls := 0
	enterpriseCalls := 0
	defaultProv := routeTrackingProvider{
		routeName: "default",
		callCount: &defaultCalls,
		response: ProviderResponse{
			Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "ok", Status: "completed", Findings: nil},
			Model:  "default", Tokens: 10, Latency: 5 * time.Millisecond,
			ResponsePayload: map[string]any{},
		},
	}
	enterpriseProv := routeTrackingProvider{
		routeName: "enterprise",
		callCount: &enterpriseCalls,
		response: ProviderResponse{
			Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "enterprise ok", Status: "completed", Findings: nil},
			Model:  "enterprise", Tokens: 20, Latency: 10 * time.Millisecond,
			ResponsePayload: map[string]any{},
		},
	}

	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	registry.Register("enterprise", enterpriseProv)

	// Test 1: When ProviderRoute is "enterprise", the enterprise provider is called.
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "enterprise"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, nil, NewDBAuditLogger(sqlDB)).WithRegistry(registry)
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-route", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun with enterprise route: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
	if enterpriseCalls != 1 {
		t.Fatalf("enterprise provider calls = %d, want 1", enterpriseCalls)
	}
	if defaultCalls != 0 {
		t.Fatalf("default provider calls = %d, want 0", defaultCalls)
	}

	// Test 2: When ProviderRoute is "default", the default provider is called.
	defaultCalls = 0
	enterpriseCalls = 0
	rulesLoader2 := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "default"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	// Seed a second run for the same MR.
	res2, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: run.ProjectID, MergeRequestID: run.MergeRequestID,
		TriggerType: "webhook", HeadSha: "head-route-default",
		Status: "pending", MaxRetries: 3,
		IdempotencyKey: fmt.Sprintf("rr-route-default-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("InsertReviewRun default: %v", err)
	}
	runID2, _ := res2.LastInsertId()
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-route-default", ID: runID2}); err != nil {
		t.Fatalf("ClaimReviewRun default: %v", err)
	}
	run2, err := q.GetReviewRun(ctx, runID2)
	if err != nil {
		t.Fatalf("GetReviewRun default: %v", err)
	}
	// Update provider responses with correct run IDs.
	defaultProv.response.Result.ReviewRunID = fmt.Sprintf("%d", runID2)
	enterpriseProv.response.Result.ReviewRunID = fmt.Sprintf("%d", runID2)
	registry2 := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	registry2.Register("enterprise", enterpriseProv)
	processor2 := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader2, nil, NewDBAuditLogger(sqlDB)).WithRegistry(registry2)
	outcome2, err := processor2.ProcessRun(ctx, run2)
	if err != nil {
		t.Fatalf("ProcessRun with default route: %v", err)
	}
	if outcome2.Status != "completed" {
		t.Fatalf("outcome2 status = %q, want completed", outcome2.Status)
	}
	if defaultCalls != 1 {
		t.Fatalf("default provider calls = %d, want 1", defaultCalls)
	}
	if enterpriseCalls != 0 {
		t.Fatalf("enterprise provider calls = %d, want 0", enterpriseCalls)
	}
}

func TestRunScopeProviderRouteOverridesPolicyRoute(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}

	defaultCalls := 0
	overrideCalls := 0
	defaultProv := routeTrackingProvider{
		routeName: "default",
		callCount: &defaultCalls,
		response: ProviderResponse{
			Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "default", Status: "completed"},
			Model:  "default",
		},
	}
	overrideProv := routeTrackingProvider{
		routeName: "openai-gpt-5-4",
		callCount: &overrideCalls,
		response: ProviderResponse{
			Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "override", Status: "completed"},
			Model:  "openai-gpt-5-4",
		},
	}

	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	registry.Register("openai-gpt-5-4", overrideProv)

	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "default"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET scope_json = ? WHERE id = ?", []byte(`{"provider_route":"openai-gpt-5-4"}`), runID); err != nil {
		t.Fatalf("seed scope_json: %v", err)
	}
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-route-override", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, nil, NewDBAuditLogger(sqlDB)).WithRegistry(registry)
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
	if overrideCalls != 1 {
		t.Fatalf("override provider calls = %d, want 1", overrideCalls)
	}
	if defaultCalls != 0 {
		t.Fatalf("default provider calls = %d, want 0", defaultCalls)
	}
}

func TestProcessRunUsesActiveFindingsForRequestedChangesStatus(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, projectID, mrID, baseRunID := seedRun(t, ctx, q)

	baseRun, err := q.GetReviewRun(ctx, baseRunID)
	if err != nil {
		t.Fatalf("GetReviewRun base: %v", err)
	}
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	baseFinding := sameRunFinding(12)
	baseFinding.Path = "src/service/foo.go"
	if err := activateSingleFinding(ctx, q, baseRun, mr, baseFinding); err != nil {
		t.Fatalf("activateSingleFinding: %v", err)
	}

	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "webhook",
		HeadSha:        "head-2",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: fmt.Sprintf("rr-active-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-active-findings", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun new: %v", err)
	}

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{
			GitLabID:  11,
			IID:       7,
			ProjectID: 101,
			Title:     "Title",
			Author: struct {
				Username string `json:"username"`
			}{Username: "alice"},
			DiffRefs:     &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head-2", StartSHA: "start"},
			HeadSHA:      "head-2",
			WebURL:       "https://gitlab.example.com/group/project/-/merge_requests/7",
			State:        "opened",
			SourceBranch: "feature",
			TargetBranch: "main",
		},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 56, BaseSHA: "base", StartSHA: "start", HeadSHA: "head-2", PatchIDSHA: "patch-2"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "src/service/foo.go", NewPath: "src/service/foo.go", Diff: "@@ -10,1 +10,1 @@\n-return *ptr\n+return *ptr\n"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	provider := &fakeProvider{response: ProviderResponse{
		Result: ReviewResult{
			SchemaVersion: "1.0",
			ReviewRunID:   fmt.Sprintf("%d", runID),
			Summary:       "summary",
			Status:        "completed",
			Findings:      []ReviewFinding{baseFinding},
		},
		Model:           "MiniMax-M2.7-highspeed",
		Tokens:          88,
		Latency:         12 * time.Millisecond,
		ResponsePayload: map[string]any{},
	}}

	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome status = %q, want requested_changes", outcome.Status)
	}
	if len(outcome.ReviewFindings) != 1 {
		t.Fatalf("outcome review findings = %d, want 1 active finding", len(outcome.ReviewFindings))
	}

	updatedRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun updated: %v", err)
	}
	if updatedRun.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", updatedRun.Status)
	}

	runRows, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun new run: %v", err)
	}
	if len(runRows) != 0 {
		t.Fatalf("new run findings = %d, want 0 when carry-forward keeps prior row active", len(runRows))
	}
}

// TestProviderRouteEndToEnd verifies that the full processor flow
// resolves the provider through the registry based on the effective
// policy's ProviderRoute, including proper audit logging.
func TestProviderRouteEndToEnd(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, mrID, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}

	projectRouteCalls := 0
	projectRouteProv := routeTrackingProvider{
		routeName: "project-custom-route",
		callCount: &projectRouteCalls,
		response: ProviderResponse{
			Result: ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   fmt.Sprintf("%d", runID),
				Summary:       "project route ok",
				Status:        "completed",
				Findings: []ReviewFinding{{
					Category: "bug", Severity: "high", Confidence: 0.9,
					Title: "Issue via project route", BodyMarkdown: "body",
					Path: "main.go", AnchorKind: "new",
				}},
			},
			Model: "project-custom-route", Tokens: 99, Latency: 15 * time.Millisecond,
			ResponsePayload: map[string]any{},
		},
	}
	defaultFallbackCalls := 0
	defaultFallbackProv := routeTrackingProvider{
		routeName: "default",
		callCount: &defaultFallbackCalls,
		response: ProviderResponse{
			Result:          ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "default ok", Status: "completed", Findings: nil},
			Model:           "default",
			ResponsePayload: map[string]any{},
		},
	}

	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultFallbackProv)
	registry.Register("project-custom-route", projectRouteProv)

	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "project-custom-route"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	processor := NewProcessor(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		sqlDB, gitlabClient, rulesLoader, nil, NewDBAuditLogger(sqlDB),
	).WithRegistry(registry)

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-e2e", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome status = %q, want requested_changes", outcome.Status)
	}
	if projectRouteCalls != 1 {
		t.Fatalf("project route calls = %d, want 1", projectRouteCalls)
	}
	if defaultFallbackCalls != 0 {
		t.Fatalf("default calls = %d, want 0 (project route should be used)", defaultFallbackCalls)
	}
	// Verify findings were persisted through the project route provider.
	findings, err := q.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Title != "Issue via project route" {
		t.Fatalf("finding title = %q, want 'Issue via project route'", findings[0].Title)
	}
	// Verify audit log captured the correct provider.
	audits, err := q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	if len(audits) == 0 {
		t.Fatal("expected audit logs")
	}
	foundProviderCall := false
	for _, audit := range audits {
		if audit.Action == "provider_called" {
			foundProviderCall = true
			var detail map[string]any
			if err := json.Unmarshal(audit.Detail, &detail); err != nil {
				t.Fatalf("unmarshal audit detail: %v", err)
			}
			if detail["provider_model"] != "project-custom-route" {
				t.Fatalf("audit provider_model = %v, want project-custom-route", detail["provider_model"])
			}
		}
	}
	if !foundProviderCall {
		t.Fatal("expected provider_called audit log")
	}
	// Verify the MR findings are present.
	activeFindings, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("ListActiveFindingsByMR: %v", err)
	}
	if len(activeFindings) != 1 {
		t.Fatalf("active findings = %d, want 1", len(activeFindings))
	}
}

// TestProviderFallbackStillWorksWithPolicyRoute proves that fallback
// behavior continues to work when routing is driven by effective policy.
// When the policy's provider route fails, the registry's fallback route
// is used.
func TestProviderFallbackStillWorksWithPolicyRoute(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}

	// Primary provider fails with a 503 error (fallback-eligible).
	primaryCalls := 0
	primaryProv := routeTrackingProvider{
		routeName: "primary-custom",
		callCount: &primaryCalls,
		err:       fmt.Errorf("provider_request_failed: upstream status 503"),
	}
	// Secondary/fallback provider succeeds.
	secondaryCalls := 0
	secondaryProv := routeTrackingProvider{
		routeName: "fallback-secondary",
		callCount: &secondaryCalls,
		response: ProviderResponse{
			Result: ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   fmt.Sprintf("%d", runID),
				Summary:       "fallback ok",
				Status:        "completed",
				Findings:      nil,
			},
			Model: "fallback-secondary", Tokens: 50, Latency: 20 * time.Millisecond,
			ResponsePayload: map[string]any{},
		},
	}

	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "primary-custom", primaryProv)
	registry.Register("fallback-secondary", secondaryProv)
	registry.SetFallbackRoute("fallback-secondary")

	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "primary-custom"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	processor := NewProcessor(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		sqlDB, gitlabClient, rulesLoader, nil, NewDBAuditLogger(sqlDB),
	).WithRegistry(registry)

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-fallback", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun with fallback: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
	// Primary was attempted, then fallback succeeded.
	if primaryCalls != 1 {
		t.Fatalf("primary provider calls = %d, want 1", primaryCalls)
	}
	if secondaryCalls != 1 {
		t.Fatalf("secondary/fallback provider calls = %d, want 1", secondaryCalls)
	}
}

// TestProviderRegistryUnknownRouteFallsBackToDefault verifies that
// requesting an unknown route from the registry returns the default.
func TestProviderRegistryUnknownRouteFallsBackToDefault(t *testing.T) {
	defaultProv := &fakeProvider{response: ProviderResponse{Model: "default-model"}}
	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)

	provider, route := registry.Resolve("unknown-route")
	if route != "default" {
		t.Fatalf("resolved route = %q, want default", route)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider for unknown route")
	}

	// Known route resolves correctly.
	customProv := &fakeProvider{response: ProviderResponse{Model: "custom-model"}}
	registry.Register("custom", customProv)
	provider2, route2 := registry.Resolve("custom")
	if route2 != "custom" {
		t.Fatalf("resolved route = %q, want custom", route2)
	}
	if provider2 == nil {
		t.Fatal("expected non-nil provider for custom route")
	}
}

// TestProviderRegistryResolveWithFallback verifies that
// ResolveWithFallback returns a FallbackProvider when fallback is configured.
func TestProviderRegistryResolveWithFallback(t *testing.T) {
	defaultProv := &fakeProvider{response: ProviderResponse{Model: "default"}}
	secondaryProv := &fakeProvider{response: ProviderResponse{Model: "secondary"}}
	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	registry.Register("secondary", secondaryProv)
	registry.SetFallbackRoute("secondary")

	// When requesting "default" route, should get a FallbackProvider.
	provider := registry.ResolveWithFallback("default")
	if _, ok := provider.(*FallbackProvider); !ok {
		t.Fatalf("expected FallbackProvider, got %T", provider)
	}

	// When requesting the fallback route itself, no wrapping needed.
	provider2 := registry.ResolveWithFallback("secondary")
	if _, ok := provider2.(*FallbackProvider); ok {
		t.Fatal("expected plain provider when route equals fallback route")
	}

	// Registry with no fallback returns plain provider.
	registry2 := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	provider3 := registry2.ResolveWithFallback("default")
	if _, ok := provider3.(*FallbackProvider); ok {
		t.Fatal("expected plain provider when no fallback is set")
	}
}

// TestProviderRegistryRoutes verifies that Routes() returns all registered routes.
func TestProviderRegistryRoutes(t *testing.T) {
	defaultProv := &fakeProvider{}
	registry := NewProviderRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), "default", defaultProv)
	registry.Register("secondary", &fakeProvider{})
	registry.Register("enterprise", &fakeProvider{})

	routes := registry.Routes()
	if len(routes) != 3 {
		t.Fatalf("routes = %v, want 3 entries", routes)
	}
	// Routes should be sorted.
	expected := []string{"default", "enterprise", "secondary"}
	for i, want := range expected {
		if routes[i] != want {
			t.Fatalf("routes[%d] = %q, want %q", i, routes[i], want)
		}
	}
}

// routeTrackingProvider is a test helper that tracks which route was
// called and how many times, proving policy-driven provider selection.
type routeTrackingProvider struct {
	routeName string
	callCount *int
	response  ProviderResponse
	err       error
}

func (p routeTrackingProvider) Review(_ context.Context, _ ctxpkg.ReviewRequest) (ProviderResponse, error) {
	*p.callCount++
	if p.err != nil {
		return ProviderResponse{}, p.err
	}
	return p.response, nil
}

func (p routeTrackingProvider) RequestPayload(_ ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"route": p.routeName}
}

// TestDegradationSummaryPersistedForWriter proves that the degradation path
// persists ErrorCode="degradation_mode" and ErrorDetail containing the
// skipped-file summary on the review_runs row, so the writer can read it.
func TestDegradationSummaryPersistedForWriter(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs: []gitlab.MergeRequestDiff{
			{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"},
			{OldPath: "other.go", NewPath: "other.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"},
		},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	project, err := q.GetProject(ctx, 1)
	if err == nil {
		_, _ = q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{
			ProjectID:           project.ID,
			ConfidenceThreshold: 0.1,
			SeverityThreshold:   "low",
			IncludePaths:        json.RawMessage("[]"),
			ExcludePaths:        json.RawMessage("[]"),
			Extra:               json.RawMessage(`{"review":{"max_files":1}}`),
		})
	}
	provider := &fakeProvider{}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}

	// Reload the run to verify persisted fields.
	updatedRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun after ProcessRun: %v", err)
	}
	if updatedRun.ErrorCode != "degradation_mode" {
		t.Fatalf("error_code = %q, want degradation_mode", updatedRun.ErrorCode)
	}
	if !updatedRun.ErrorDetail.Valid {
		t.Fatal("error_detail is NULL, want non-null degradation summary")
	}
	if !strings.Contains(updatedRun.ErrorDetail.String, "降级审查模式") {
		t.Fatalf("error_detail missing degradation mode text: %s", updatedRun.ErrorDetail.String)
	}
}

// TestDegradationSummaryNoteIncludesSkippedFiles proves that the degradation
// summary note persisted on the run includes the specific skipped files and
// reasons, not just a generic message.
func TestDegradationSummaryNoteIncludesSkippedFiles(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs: []gitlab.MergeRequestDiff{
			{OldPath: "priority.go", NewPath: "priority.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"},
			{OldPath: "skipped.go", NewPath: "skipped.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"},
			{OldPath: "also_skipped.go", NewPath: "also_skipped.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"},
		},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	project, err := q.GetProject(ctx, 1)
	if err == nil {
		_, _ = q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{
			ProjectID:           project.ID,
			ConfidenceThreshold: 0.1,
			SeverityThreshold:   "low",
			IncludePaths:        json.RawMessage("[]"),
			ExcludePaths:        json.RawMessage("[]"),
			Extra:               json.RawMessage(`{"review":{"max_files":1}}`),
		})
	}
	provider := &fakeProvider{}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	_, err = processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}

	updatedRun, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if !updatedRun.ErrorDetail.Valid {
		t.Fatal("error_detail is NULL, want skipped files")
	}
	detail := updatedRun.ErrorDetail.String
	// The summary must mention skipped files with their reasons.
	if !strings.Contains(detail, "已跳过的文件") {
		t.Fatalf("error_detail missing skipped-files section: %s", detail)
	}
	if !strings.Contains(detail, "skipped.go") || !strings.Contains(detail, "also_skipped.go") {
		t.Fatalf("error_detail missing skipped file names: %s", detail)
	}
	if !strings.Contains(detail, "scope_limit") {
		t.Fatalf("error_detail missing scope_limit reason: %s", detail)
	}
}

func TestParseSummaryResult(t *testing.T) {
	raw := `{"schema_version":"1.0","review_run_id":"rr-1","walkthrough":"This MR adds a new endpoint for user registration.","risk_areas":[{"path":"src/auth/register.go","description":"No input validation on email field","severity":"high"}],"blind_spots":["Integration tests not reviewed"],"verdict":"comment"}`
	result, err := ParseSummaryResult(raw)
	if err != nil {
		t.Fatalf("ParseSummaryResult: %v", err)
	}
	if result.SchemaVersion != "1.0" {
		t.Fatalf("schema_version = %q, want 1.0", result.SchemaVersion)
	}
	if result.ReviewRunID != "rr-1" {
		t.Fatalf("review_run_id = %q, want rr-1", result.ReviewRunID)
	}
	if result.Walkthrough != "This MR adds a new endpoint for user registration." {
		t.Fatalf("walkthrough = %q", result.Walkthrough)
	}
	if len(result.RiskAreas) != 1 || result.RiskAreas[0].Path != "src/auth/register.go" {
		t.Fatalf("risk_areas = %#v", result.RiskAreas)
	}
	if len(result.BlindSpots) != 1 || result.BlindSpots[0] != "Integration tests not reviewed" {
		t.Fatalf("blind_spots = %#v", result.BlindSpots)
	}
	if result.Verdict != "comment" {
		t.Fatalf("verdict = %q, want comment", result.Verdict)
	}
}

func TestParseSummaryResultFallback(t *testing.T) {
	t.Run("markdown wrapper", func(t *testing.T) {
		raw := "Here is the summary:\n```json\n{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-2\",\"walkthrough\":\"Refactored error handling.\",\"verdict\":\"approve\"}\n```"
		result, err := ParseSummaryResult(raw)
		if err != nil {
			t.Fatalf("ParseSummaryResult: %v", err)
		}
		if result.Walkthrough != "Refactored error handling." {
			t.Fatalf("walkthrough = %q", result.Walkthrough)
		}
		if result.Verdict != "approve" {
			t.Fatalf("verdict = %q, want approve", result.Verdict)
		}
	})
	t.Run("tolerant repair", func(t *testing.T) {
		raw := `{"schema_version":"1.0","review_run_id":"rr-3","walkthrough":"Fixed tests.","verdict":"approve",}`
		result, err := ParseSummaryResult(raw)
		if err != nil {
			t.Fatalf("ParseSummaryResult: %v", err)
		}
		if result.Walkthrough != "Fixed tests." {
			t.Fatalf("walkthrough = %q", result.Walkthrough)
		}
	})
	t.Run("empty input", func(t *testing.T) {
		_, err := ParseSummaryResult("")
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseSummaryResult("definitely not json")
		if err == nil {
			t.Fatal("expected error for invalid json")
		}
	})
	t.Run("missing required fields", func(t *testing.T) {
		raw := `{"schema_version":"1.0","review_run_id":"rr-4","verdict":"approve"}`
		_, err := ParseSummaryResult(raw)
		if err == nil {
			t.Fatal("expected error when walkthrough is missing")
		}
	})
	t.Run("missing verdict", func(t *testing.T) {
		raw := `{"schema_version":"1.0","review_run_id":"rr-5","walkthrough":"Fixed tests."}`
		_, err := ParseSummaryResult(raw)
		if err == nil {
			t.Fatal("expected error when verdict is missing")
		}
	})
}

func TestBuildReviewRepairPayloadIncludesExplicitRepairGuidance(t *testing.T) {
	raw := buildReviewRepairPayload(
		ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"},
		`{"bad":true}`,
		errors.New("$.findings[0].body_markdown is required"),
	)
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	instructions, ok := payload["instructions"].([]any)
	if !ok {
		t.Fatalf("instructions = %#v", payload["instructions"])
	}
	text := fmt.Sprint(instructions...)
	for _, want := range []string{
		"Call submit_review exactly once.",
		"Preserve every valid field from the original tool input.",
		"Fill every missing required field called out by validation_error.",
		"Do not emit markdown fences or free-form prose.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions missing %q in %v", want, instructions)
		}
	}
}

func TestRenderSummaryFromWalkthrough(t *testing.T) {
	t.Run("full summary", func(t *testing.T) {
		summary := SummaryResult{
			SchemaVersion: "1.0",
			ReviewRunID:   "rr-1",
			Walkthrough:   "This MR refactors the auth module.",
			RiskAreas: []RiskArea{
				{Path: "src/auth/login.go", Description: "Session handling changed", Severity: "high"},
				{Path: "src/auth/token.go", Description: "Token rotation logic", Severity: "medium"},
			},
			BlindSpots: []string{"No integration tests for SSO flow"},
			Verdict:    "request_changes",
		}
		got := renderSummaryFromWalkthrough(summary, reviewlang.DefaultOutputLanguage)
		if !strings.Contains(got, "## 变更解读") {
			t.Fatal("missing walkthrough header")
		}
		if !strings.Contains(got, "This MR refactors the auth module.") {
			t.Fatal("missing walkthrough body")
		}
		if !strings.Contains(got, "### 风险区域") {
			t.Fatal("missing risk areas section")
		}
		if !strings.Contains(got, "**src/auth/login.go**（high）") {
			t.Fatal("missing first risk area")
		}
		if !strings.Contains(got, "**src/auth/token.go**（medium）") {
			t.Fatal("missing second risk area")
		}
		if !strings.Contains(got, "### 盲区") {
			t.Fatal("missing blind spots section")
		}
		if !strings.Contains(got, "No integration tests for SSO flow") {
			t.Fatal("missing blind spot")
		}
		if !strings.Contains(got, "**结论**：request_changes") {
			t.Fatal("missing verdict")
		}
	})
	t.Run("minimal summary", func(t *testing.T) {
		summary := SummaryResult{
			SchemaVersion: "1.0",
			ReviewRunID:   "rr-2",
			Walkthrough:   "Minor fix.",
			Verdict:       "approve",
		}
		got := renderSummaryFromWalkthrough(summary, "en-US")
		if !strings.Contains(got, "## MR Walkthrough") {
			t.Fatal("missing walkthrough header")
		}
		if !strings.Contains(got, "Minor fix.") {
			t.Fatal("missing walkthrough body")
		}
		if strings.Contains(got, "### Risk Areas") {
			t.Fatal("unexpected risk areas section for empty risk areas")
		}
		if strings.Contains(got, "### Blind Spots") {
			t.Fatal("unexpected blind spots section for empty blind spots")
		}
		if !strings.Contains(got, "**Verdict**: approve") {
			t.Fatal("missing verdict")
		}
	})
}

func TestProcessRunWithSummaryProvider(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	provider := &fakeProvider{response: ProviderResponse{
		Result:          ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: nil},
		Model:           "MiniMax-M2.5",
		Tokens:          50,
		Latency:         10 * time.Millisecond,
		ResponsePayload: map[string]any{},
	}}
	summaryProv := &fakeSummaryProvider{response: SummaryResponse{
		Result: SummaryResult{
			SchemaVersion: "1.0",
			ReviewRunID:   fmt.Sprintf("%d", runID),
			Walkthrough:   "This MR updates the main module.",
			RiskAreas:     []RiskArea{{Path: "main.go", Description: "Core logic changed", Severity: "medium"}},
			Verdict:       "approve",
		},
		Latency: 5 * time.Millisecond,
		Tokens:  30,
		Model:   "summary-model",
	}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB)).WithSummaryProvider(summaryProv)

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-summary", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
	if !summaryProv.called {
		t.Fatal("expected summary provider to be called")
	}
	// Verify audit log has summary_chain_completed entry.
	audits, err := q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 20, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	foundSummaryAudit := false
	for _, audit := range audits {
		if audit.Action == "summary_chain_completed" {
			foundSummaryAudit = true
		}
	}
	if !foundSummaryAudit {
		t.Fatal("expected summary_chain_completed audit log")
	}
}

func TestProcessRunWithSummaryProviderFailure(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	provider := &fakeProvider{response: ProviderResponse{
		Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{{
			Category: "bug", Severity: "high", Confidence: 0.9, Title: "Issue", BodyMarkdown: "body", Path: "main.go", AnchorKind: "new",
		}}},
		Model: "MiniMax-M2.5", Tokens: 50, Latency: 10 * time.Millisecond, ResponsePayload: map[string]any{},
	}}
	// Summary provider that fails — should not block the review.
	summaryProv := &fakeSummaryProvider{err: fmt.Errorf("summary provider error")}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB)).WithSummaryProvider(summaryProv)

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-summary-fail", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun should succeed even when summary fails: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome status = %q, want requested_changes", outcome.Status)
	}
	if !summaryProv.called {
		t.Fatal("expected summary provider to be called")
	}
}

func TestProcessRunWithoutSummaryProvider(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform"}}}
	provider := &fakeProvider{response: ProviderResponse{
		Result:          ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: nil},
		Model:           "MiniMax-M2.5",
		Tokens:          50,
		Latency:         10 * time.Millisecond,
		ResponsePayload: map[string]any{},
	}}
	// No summary provider — should behave exactly as before.
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-no-summary", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
}

func TestProcessRunContinuesWhenOutputLanguagePersistenceFails(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, providerTestMigrationsDir)
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)

	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET scope_json = ? WHERE id = ?", []byte("[]"), runID); err != nil {
		t.Fatalf("seed invalid scope_json: %v", err)
	}

	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title",
			Author: struct {
				Username string "json:\"username\""
			}{Username: "alice"},
			DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}},
	}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{
		EffectivePolicy: rules.EffectivePolicy{OutputLanguage: "zh-CN"},
		Trusted:         ctxpkg.TrustedRules{PlatformPolicy: "platform"},
	}}
	provider := &fakeProvider{response: ProviderResponse{
		Result:          ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: nil},
		Model:           "MiniMax-M2.5",
		Tokens:          50,
		Latency:         10 * time.Millisecond,
		ResponsePayload: map[string]any{},
	}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))

	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-output-language", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun should continue when scope metadata persistence fails: %v", err)
	}
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
	}
}

type fakeSummaryProvider struct {
	response SummaryResponse
	err      error
	called   bool
}

func (f *fakeSummaryProvider) Summarize(_ context.Context, _ ctxpkg.ReviewRequest) (SummaryResponse, error) {
	f.called = true
	return f.response, f.err
}

func TestRecordProviderMetricsWithSubProviders(t *testing.T) {
	reg := metrics.NewRegistry()
	p := &Processor{metrics: reg}
	response := ProviderResponse{
		Latency: 100 * time.Millisecond,
		Tokens:  500,
		SubProviderResults: []SubProviderResult{
			{RouteName: "openai", Model: "gpt-4o", Latency: 80 * time.Millisecond, Tokens: 300, Status: "success"},
			{RouteName: "anthropic", Model: "claude-sonnet", Latency: 90 * time.Millisecond, Tokens: 200, Status: "success"},
		},
	}
	p.recordProviderMetrics(response)

	if v := reg.CounterValue("provider_tokens_total", nil); v != 500 {
		t.Fatalf("provider_tokens_total = %d, want 500", v)
	}
	if v := reg.CounterValue("sub_provider_tokens_total", map[string]string{"route": "openai", "model": "gpt-4o", "status": "success"}); v != 300 {
		t.Fatalf("sub_provider_tokens_total(openai) = %d, want 300", v)
	}
	if v := reg.CounterValue("sub_provider_tokens_total", map[string]string{"route": "anthropic", "model": "claude-sonnet", "status": "success"}); v != 200 {
		t.Fatalf("sub_provider_tokens_total(anthropic) = %d, want 200", v)
	}
	hist := reg.HistogramValues("sub_provider_latency_ms", map[string]string{"route": "openai", "model": "gpt-4o", "status": "success"})
	if len(hist) != 1 || hist[0] != 80 {
		t.Fatalf("sub_provider_latency_ms(openai) = %v, want [80]", hist)
	}
	hist = reg.HistogramValues("sub_provider_latency_ms", map[string]string{"route": "anthropic", "model": "claude-sonnet", "status": "success"})
	if len(hist) != 1 || hist[0] != 90 {
		t.Fatalf("sub_provider_latency_ms(anthropic) = %v, want [90]", hist)
	}
	if hist := reg.HistogramValues("provider_latency_ms", nil); len(hist) != 1 || hist[0] != 100 {
		t.Fatalf("provider_latency_ms = %v, want [100]", hist)
	}
}

func TestRecordProviderMetricsWithoutSubProviders(t *testing.T) {
	reg := metrics.NewRegistry()
	p := &Processor{metrics: reg}
	response := ProviderResponse{
		Latency: 50 * time.Millisecond,
		Tokens:  100,
	}
	p.recordProviderMetrics(response)

	if v := reg.CounterValue("provider_tokens_total", nil); v != 100 {
		t.Fatalf("provider_tokens_total = %d, want 100", v)
	}
	if hist := reg.HistogramValues("provider_latency_ms", nil); len(hist) != 1 || hist[0] != 50 {
		t.Fatalf("provider_latency_ms = %v, want [50]", hist)
	}
	// No sub_provider metrics should exist
	if v := reg.HistogramValues("sub_provider_latency_ms", nil); v != nil {
		t.Fatalf("unexpected sub_provider histogram = %v", v)
	}
}

var _ = option.WithAPIKey
var _ ssestream.Event
