package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/rules"
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
	rulesLoader := fakeRulesLoader{result: rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform", ProjectPolicy: "project", ReviewMarkdown: "review", RulesDigest: "digest"}}}
	provider := fakeProvider{response: ProviderResponse{Result: ReviewResult{SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID), Summary: "summary", Status: "completed", Findings: []ReviewFinding{{Category: "bug", Severity: "high", Confidence: 0.9, Title: "Issue", BodyMarkdown: "body", Path: "main.go", AnchorKind: "new"}}}, Model: "MiniMax-M2.5", Tokens: 77, Latency: 25 * time.Millisecond, ResponsePayload: map[string]any{"token": "secret", "content": "prompt body"}}}
	processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, gitlabClient, rulesLoader, provider, NewDBAuditLogger(sqlDB))
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}
	run, err = q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if err := processor.ProcessRun(ctx, run); err != nil {
		t.Fatalf("ProcessRun: %v", err)
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

type fakeRulesLoader struct{ result rules.LoadResult }

func (f fakeRulesLoader) Load(context.Context, rules.LoadInput) (rules.LoadResult, error) {
	return f.result, nil
}

type fakeProvider struct {
	response ProviderResponse
	err      error
}

func (f fakeProvider) Review(context.Context, ctxpkg.ReviewRequest) (ProviderResponse, error) {
	return f.response, f.err
}
func (f fakeProvider) RequestPayload(ctxpkg.ReviewRequest) map[string]any {
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

var _ = option.WithAPIKey
var _ ssestream.Event
