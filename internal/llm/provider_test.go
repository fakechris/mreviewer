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

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	metrics2 "github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

func TestMiniMaxRequestShape(t *testing.T) {
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"text","text":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}],"usage":{"output_tokens":42}}`}
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
	if response.Tokens != 42 {
		t.Fatalf("tokens = %d, want 42", response.Tokens)
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
	if _, ok := payload["system"]; !ok {
		t.Fatal("missing system prompt")
	}
	if _, ok := payload["output_config"]; !ok {
		t.Fatal("missing output_config")
	}
	if got := transport.header.Get("X-Api-Key"); got != "secret-token" {
		t.Fatalf("x-api-key = %q", got)
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

func TestLLMRateLimiting(t *testing.T) {
	var slept []time.Duration
	current := time.Unix(0, 0)
	limiter := NewInMemoryRateLimiter(RateLimitConfig{Requests: 1, Window: time.Second}, func() time.Time { return current }, func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		current = current.Add(delay)
		return nil
	})
	transport := &captureTransport{responseBody: `{"id":"msg_1","content":[{"type":"text","text":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}],"usage":{"output_tokens":1}}`}
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
	payload := map[string]any{"api_key": "secret", "Authorization": "Bearer abc", "messages": []any{map[string]any{"content": "very long prompt body"}}, "diff": stringsRepeat("x", 300)}
	redacted := redactPayload(payload)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"secret", "Bearer abc", "very long prompt body"} {
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

func TestWorkerExecutesRealProcessor(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
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
	if updatedRun.Status != "completed" {
		t.Fatalf("status = %q, want completed", updatedRun.Status)
	}
	audits, err := q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	if len(audits) == 0 {
		t.Fatal("expected provider audit log")
	}
	detail := string(audits[0].Detail)
	for _, forbidden := range []string{"secret", "prompt body"} {
		if stringsContains(detail, forbidden) {
			t.Fatalf("audit detail leaked %q: %s", forbidden, detail)
		}
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

func TestWorkerThreadsPerPathReviewIntoReviewRequest(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
	q := db.New(sqlDB)
	_, _, _, runID := seedRun(t, ctx, q)
	gitlabClient := &fakeGitLabReader{snapshot: gitlab.MergeRequestSnapshot{MergeRequest: gitlab.MergeRequest{GitLabID: 11, IID: 7, ProjectID: 101, Title: "Title", Author: struct {
		Username string "json:\"username\""
	}{Username: "alice"}, DiffRefs: &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}}, Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"}, Diffs: []gitlab.MergeRequestDiff{{OldPath: "main.go", NewPath: "main.go", Diff: "@@ -1,1 +1,2 @@\n line1\n+line2"}}}}
	rulesLoader := &fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"}}}
	provider := &fakeProvider{err: scheduler.NewTerminalError("parser_error", errors.New("unparseable provider output"))}
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
		{name: "posted to active", current: findingStatePosted, next: findingStateActive, wantOK: true},
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	body         bytes.Buffer
	header       http.Header
	responseBody string
	errSequence  []error
	calls        int
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
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewBufferString(t.responseBody))}, nil
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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

// TestProviderRouteEndToEnd verifies that the full processor flow
// resolves the provider through the registry based on the effective
// policy's ProviderRoute, including proper audit logging.
func TestProviderRouteEndToEnd(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	if outcome.Status != "completed" {
		t.Fatalf("outcome status = %q, want completed", outcome.Status)
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	if !strings.Contains(updatedRun.ErrorDetail.String, "degradation mode") {
		t.Fatalf("error_detail missing degradation mode text: %s", updatedRun.ErrorDetail.String)
	}
}

// TestDegradationSummaryNoteIncludesSkippedFiles proves that the degradation
// summary note persisted on the run includes the specific skipped files and
// reasons, not just a generic message.
func TestDegradationSummaryNoteIncludesSkippedFiles(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
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
	if !strings.Contains(detail, "Skipped files") {
		t.Fatalf("error_detail missing 'Skipped files' section: %s", detail)
	}
	if !strings.Contains(detail, "skipped.go") || !strings.Contains(detail, "also_skipped.go") {
		t.Fatalf("error_detail missing skipped file names: %s", detail)
	}
	if !strings.Contains(detail, "scope_limit") {
		t.Fatalf("error_detail missing scope_limit reason: %s", detail)
	}
}

var _ = option.WithAPIKey
var _ ssestream.Event
