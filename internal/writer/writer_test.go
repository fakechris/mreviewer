package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/metrics"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

func TestPositionUsesVersionSHAs(t *testing.T) {
	position := BuildPosition(db.MrVersion{BaseSha: "version-base", StartSha: "version-start", HeadSha: "version-head"}, db.ReviewFinding{Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 14, Valid: true}})
	if position.BaseSHA != "version-base" || position.StartSHA != "version-start" || position.HeadSHA != "version-head" {
		t.Fatalf("position SHAs = %+v, want version SHAs", position)
	}
}

func TestOldAndNewPathPopulation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantOld string
		wantNew string
	}{
		{name: "modified", path: "pkg/file.go", wantOld: "pkg/file.go", wantNew: "pkg/file.go"},
		{name: "new file", path: "pkg/new.go", wantOld: "pkg/new.go", wantNew: "pkg/new.go"},
		{name: "deleted file", path: "pkg/old.go", wantOld: "pkg/old.go", wantNew: "pkg/old.go"},
		{name: "renamed file", path: "pkg/old.go -> pkg/new.go", wantOld: "pkg/old.go", wantNew: "pkg/new.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := BuildPosition(db.MrVersion{}, db.ReviewFinding{Path: tt.path, AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 8, Valid: true}})
			if position.OldPath != tt.wantOld || position.NewPath != tt.wantNew {
				t.Fatalf("paths = (%q,%q), want (%q,%q)", position.OldPath, position.NewPath, tt.wantOld, tt.wantNew)
			}
		})
	}
}

func TestAnchorKindLineTargeting(t *testing.T) {
	tests := []struct {
		name        string
		anchorKind  string
		oldLine     sql.NullInt32
		newLine     sql.NullInt32
		wantOldLine bool
		wantNewLine bool
	}{
		{name: "added line", anchorKind: "new_line", newLine: sql.NullInt32{Int32: 10, Valid: true}, wantNewLine: true},
		{name: "removed line", anchorKind: "old_line", oldLine: sql.NullInt32{Int32: 11, Valid: true}, wantOldLine: true},
		{name: "context line", anchorKind: "context_line", oldLine: sql.NullInt32{Int32: 12, Valid: true}, newLine: sql.NullInt32{Int32: 13, Valid: true}, wantOldLine: true, wantNewLine: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := BuildPosition(db.MrVersion{}, db.ReviewFinding{Path: "pkg/file.go", AnchorKind: tt.anchorKind, OldLine: tt.oldLine, NewLine: tt.newLine})
			if (position.OldLine != nil) != tt.wantOldLine {
				t.Fatalf("old line presence = %v, want %v", position.OldLine != nil, tt.wantOldLine)
			}
			if (position.NewLine != nil) != tt.wantNewLine {
				t.Fatalf("new line presence = %v, want %v", position.NewLine != nil, tt.wantNewLine)
			}
		})
	}
}

func TestCommentBodyTemplate(t *testing.T) {
	body := RenderCommentBody(db.ReviewFinding{ID: 42, Title: "Possible nil dereference", Confidence: 0.91, BodyMarkdown: sql.NullString{String: "This branch dereferences `user.profile` without a guard.", Valid: true}, Evidence: sql.NullString{String: "`user.profile` is optional\nThis branch runs before fallback logic", Valid: true}, SuggestedPatch: sql.NullString{String: "Guard `user.profile` before reading `timezone`.", Valid: true}, AnchorFingerprint: "anchor-fp", SemanticFingerprint: "semantic-fp"})
	checks := []string{"**Possible nil dereference**", "This branch dereferences `user.profile` without a guard.", "Evidence:", "- `user.profile` is optional", "Suggested fix:", "Guard `user.profile` before reading `timezone`.", "<!-- ai-review:finding_id=42 anchor_fp=anchor-fp semantic_fp=semantic-fp confidence=0.91 -->"}
	for _, check := range checks {
		if !contains(body, check) {
			t.Fatalf("body missing %q:\n%s", check, body)
		}
	}
}

func TestSameLineSeparateDiscussions(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	w.now = func() time.Time { return time.Unix(1, 0) }
	findings := []db.ReviewFinding{
		{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8},
		{ID: 2, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue two", Confidence: 0.85},
	}
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99}, findings); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("discussion requests = %d, want 2", len(client.requests))
	}
	if client.requests[0].ReviewFindingID == client.requests[1].ReviewFindingID {
		t.Fatalf("expected separate discussions for separate findings")
	}
	if len(store.insertedDiscussions) != 2 {
		t.Fatalf("stored discussions = %d, want 2", len(store.insertedDiscussions))
	}
}

func TestDiffFallbackToFile(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{errors.New("400 invalid position")}}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("discussion requests = %d, want 2", len(client.requests))
	}
	if client.requests[1].Position.PositionType != "file" {
		t.Fatalf("fallback position type = %q, want file", client.requests[1].Position.PositionType)
	}
	if !contains(client.requests[1].Body, "Original target line: new_line=17") {
		t.Fatalf("file fallback body missing original target line: %s", client.requests[1].Body)
	}
	if len(client.noteRequests) != 0 {
		t.Fatalf("note requests = %d, want 0", len(client.noteRequests))
	}
}

func TestFileFallbackToGeneralNote(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{errors.New("400 invalid position"), errors.New("400 invalid position")}}
	w := New(client, store)
	finding := db.ReviewFinding{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8, AnchorSnippet: sql.NullString{String: "if bad == nil {", Valid: true}}
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{finding}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.noteRequests) != 1 {
		t.Fatalf("note requests = %d, want 1", len(client.noteRequests))
	}
	if !contains(client.noteRequests[0].Body, "File: `pkg/file.go`") || !contains(client.noteRequests[0].Body, "Anchor context:") {
		t.Fatalf("general note body missing fallback context: %s", client.noteRequests[0].Body)
	}
}

func TestParserErrorSingleNote(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{}
	registry := metrics.NewRegistry()
	w := New(client, store).WithMetrics(registry)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "parser_error"}, []db.ReviewFinding{{ID: 1}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("discussion requests = %d, want 0", len(client.requests))
	}
	if len(client.noteRequests) != 1 {
		t.Fatalf("note requests = %d, want 1", len(client.noteRequests))
	}
	if len(store.insertedDiscussions) != 0 {
		t.Fatalf("stored discussions = %d, want 0", len(store.insertedDiscussions))
	}
	if got := registry.HistogramValues("comment_writer_latency_ms", map[string]string{"status": "parser_error"}); len(got) != 1 {
		t.Fatalf("writer latency samples = %v, want 1 parser_error sample", got)
	}
}

func TestRunSummaryNote(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{}
	registry := metrics.NewRegistry()
	tracer := tracing.NewRecorder()
	w := New(client, store).WithMetrics(registry).WithTracer(tracer)
	finding := db.ReviewFinding{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8, State: "new"}
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, []db.ReviewFinding{finding}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.noteRequests) != 1 {
		t.Fatalf("note requests = %d, want 1", len(client.noteRequests))
	}
	if !contains(client.noteRequests[0].Body, "overall_risk: elevated") {
		t.Fatalf("summary note missing risk summary: %s", client.noteRequests[0].Body)
	}
	if store.actions["run:55:summary_note"].ActionType != actionTypeSummaryNote {
		t.Fatalf("summary action type = %q, want %q", store.actions["run:55:summary_note"].ActionType, actionTypeSummaryNote)
	}
	if got := registry.HistogramValues("comment_writer_latency_ms", map[string]string{"status": "completed"}); len(got) != 1 {
		t.Fatalf("writer latency samples = %v, want 1 sample", got)
	}
	if spans := tracer.Spans(); len(spans) == 0 || spans[0].Name != "gitlab.create_discussion" {
		t.Fatalf("writer spans = %+v, expected gitlab.create_discussion span", spans)
	}
}

func TestEmptyFindingsSummary(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("discussion requests = %d, want 0", len(client.requests))
	}
	if len(client.noteRequests) != 1 {
		t.Fatalf("note requests = %d, want 1", len(client.noteRequests))
	}
	if !contains(client.noteRequests[0].Body, "No issues found.") {
		t.Fatalf("empty summary note missing clean-run text: %s", client.noteRequests[0].Body)
	}
}

func TestDiscussionIdPersistedToFinding(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, findingsByID: map[int64]db.ReviewFinding{1: {ID: 1}}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := store.findingsByID[1].GitlabDiscussionID; got == "" {
		t.Fatal("finding discussion id was not persisted")
	}
	if got := store.findingsByID[1].GitlabDiscussionID; got != "run:55:finding:1:create_discussion" {
		t.Fatalf("finding discussion id = %q, want persisted created discussion id", got)
	}
}

func TestAutoResolveFixedOrStale(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, findingsByRun: map[int64][]db.ReviewFinding{55: {{ID: 1, State: "fixed"}, {ID: 2, State: "stale"}}}, discussionByID: map[int64]db.GitlabDiscussion{1: {ID: 11, GitlabDiscussionID: "disc-1", DiscussionType: "diff"}, 2: {ID: 12, GitlabDiscussionID: "disc-2", DiscussionType: "diff"}}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.resolveRequests) != 2 {
		t.Fatalf("resolve requests = %d, want 2", len(client.resolveRequests))
	}
	if !store.resolvedDiscussionUpdates[11] || !store.resolvedDiscussionUpdates[12] {
		t.Fatalf("resolved discussion updates = %+v, want ids 11 and 12 resolved", store.resolvedDiscussionUpdates)
	}
	if store.actions["run:55:finding:1:resolve_discussion"].Status != commentActionStatusSucceeded {
		t.Fatalf("fixed resolve action status = %q, want succeeded", store.actions["run:55:finding:1:resolve_discussion"].Status)
	}
}

func TestAlreadyResolvedDiscussion(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, findingsByRun: map[int64][]db.ReviewFinding{55: {{ID: 1, State: "fixed"}}}, discussionByID: map[int64]db.GitlabDiscussion{1: {ID: 11, GitlabDiscussionID: "disc-1", DiscussionType: "diff", Resolved: true}}}
	client := &fakeDiscussionClient{resolveErrors: []error{errors.New("already resolved")}}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	action := store.actions["run:55:finding:1:resolve_discussion"]
	if action.Status != commentActionStatusSucceeded {
		t.Fatalf("resolve action status = %q, want succeeded", action.Status)
	}
	if action.ErrorCode != "" {
		t.Fatalf("resolve action error code = %q, want empty", action.ErrorCode)
	}
	if !store.resolvedDiscussionUpdates[11] {
		t.Fatalf("discussion 11 not marked resolved in store")
	}
}

func TestSupersedeResolvesOldDiscussion(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, findingsByRun: map[int64][]db.ReviewFinding{55: {{ID: 2, State: "new", GitlabDiscussionID: "disc-new"}, {ID: 1, State: "superseded", MatchedFindingID: sql.NullInt64{Int64: 2, Valid: true}}}}, findingsByID: map[int64]db.ReviewFinding{1: {ID: 1}, 2: {ID: 2, GitlabDiscussionID: "disc-new"}}, discussionByID: map[int64]db.GitlabDiscussion{1: {ID: 11, ReviewFindingID: 1, MergeRequestID: 99, GitlabDiscussionID: "disc-old", DiscussionType: "diff"}, 2: {ID: 22, ReviewFindingID: 2, MergeRequestID: 99, GitlabDiscussionID: "disc-new", DiscussionType: "diff"}}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(client.resolveRequests) != 1 {
		t.Fatalf("resolve requests = %d, want 1", len(client.resolveRequests))
	}
	if !store.resolvedDiscussionUpdates[11] {
		t.Fatalf("old discussion not marked resolved")
	}
	if got := store.discussionByID[1].SupersededByDiscussionID; !got.Valid || got.Int64 != 22 {
		t.Fatalf("superseded_by_discussion_id = %+v, want valid replacement discussion row id 22", got)
	}
}

func TestThreadsResolvedGateDefault(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, findingsByRun: map[int64][]db.ReviewFinding{56: {{ID: 1, State: "fixed"}}}, discussionByID: map[int64]db.GitlabDiscussion{1: {ID: 11, GitlabDiscussionID: "disc-1", DiscussionType: "diff"}}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	activeFinding := db.ReviewFinding{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8, State: "new"}
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "completed"}, []db.ReviewFinding{activeFinding}); err != nil {
		t.Fatalf("initial Write: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(client.requests))
	}
	if err := w.Write(context.Background(), db.ReviewRun{ID: 56, MergeRequestID: 99, Status: "completed"}, nil); err != nil {
		t.Fatalf("follow-up Write: %v", err)
	}
	if len(client.resolveRequests) != 1 {
		t.Fatalf("resolve requests = %d, want 1", len(client.resolveRequests))
	}
}

func TestCommentIdempotency(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{}
	w := New(client, store)
	run := db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}
	findings := []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}}
	if err := w.Write(context.Background(), run, findings); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := w.Write(context.Background(), run, findings); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(client.requests))
	}
}

func TestResumeAfterPartialBatch(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{nil, errors.New("400 invalid position"), errors.New("temporary outage"), errors.New("temporary outage"), errors.New("temporary outage")}}
	w := New(client, store)
	run := db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}
	findings := []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}, {ID: 2, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 19, Valid: true}, Title: "Issue two", Confidence: 0.8}}
	err := w.Write(context.Background(), run, findings)
	if err == nil {
		t.Fatal("expected first Write to fail")
	}
	client.discussionErrors = nil
	if err := w.Write(context.Background(), run, findings); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if len(client.requests) != 6 {
		t.Fatalf("discussion requests = %d, want 6", len(client.requests))
	}
	if client.requests[5].ReviewFindingID != 2 {
		t.Fatalf("resumed finding id = %d, want 2", client.requests[5].ReviewFindingID)
	}
}

func TestWriterRetryBackoff(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{retryableHTTPError(429), retryableHTTPError(503), nil}}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if client.discussionCalls != 3 {
		t.Fatalf("discussion calls = %d, want 3", client.discussionCalls)
	}
}

func TestFailureReasonCodes(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{errors.New("temporary outage"), errors.New("temporary outage"), errors.New("temporary outage")}}
	w := New(client, store)
	err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}})
	if err == nil {
		t.Fatal("expected Write to fail")
	}
	action := store.actions["run:55:finding:1:create_discussion"]
	if action.ErrorCode != writerErrorUnavailable {
		t.Fatalf("action error code = %q, want %q", action.ErrorCode, writerErrorUnavailable)
	}
	if store.runUpdates[55].ErrorCode != writerErrorUnavailable {
		t.Fatalf("run error code = %q, want %q", store.runUpdates[55].ErrorCode, writerErrorUnavailable)
	}
	if store.runUpdates[55].Status != "failed" {
		t.Fatalf("run status = %q, want failed", store.runUpdates[55].Status)
	}
}

func TestWriterFailureSetsRunFailed(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, runsByID: map[int64]db.ReviewRun{55: {ID: 55, MergeRequestID: 99, Status: "running"}}}
	client := &fakeDiscussionClient{discussionErrors: []error{errors.New("temporary outage"), errors.New("temporary outage"), errors.New("temporary outage")}}
	w := New(client, store)
	err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}})
	if err == nil {
		t.Fatal("expected Write to fail")
	}
	run := store.runsByID[55]
	if run.Status != "failed" {
		t.Fatalf("persisted run status = %q, want failed", run.Status)
	}
	if run.ErrorCode != writerErrorUnavailable {
		t.Fatalf("persisted run error code = %q, want %q", run.ErrorCode, writerErrorUnavailable)
	}
	if !run.ErrorDetail.Valid || !contains(run.ErrorDetail.String, "temporary outage") {
		t.Fatalf("persisted run error detail = %+v, want temporary outage detail", run.ErrorDetail)
	}
}

func TestParserErrorNoteFailureSetsRunFailed(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}, runsByID: map[int64]db.ReviewRun{55: {ID: 55, MergeRequestID: 99, Status: "running"}}}
	client := &fakeDiscussionClient{noteErrors: []error{errors.New("parser fallback note unavailable"), errors.New("parser fallback note unavailable"), errors.New("parser fallback note unavailable")}}
	w := New(client, store)
	err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "parser_error"}, nil)
	if err == nil {
		t.Fatal("expected Write to fail")
	}
	run := store.runsByID[55]
	if run.Status != "failed" {
		t.Fatalf("persisted run status = %q, want failed", run.Status)
	}
	if run.ErrorCode != writerErrorParserFallback {
		t.Fatalf("persisted run error code = %q, want %q", run.ErrorCode, writerErrorParserFallback)
	}
	if !run.ErrorDetail.Valid || !contains(run.ErrorDetail.String, "parser fallback note unavailable") {
		t.Fatalf("persisted run error detail = %+v, want parser fallback note error", run.ErrorDetail)
	}
}

func TestGitLabOutageRecovery(t *testing.T) {
	store := &fakeStore{mr: db.MergeRequest{ID: 99, ProjectID: 123, MrIid: 7}, version: db.MrVersion{BaseSha: "base", StartSha: "start", HeadSha: "head"}}
	client := &fakeDiscussionClient{discussionErrors: []error{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, &net.OpError{Op: "dial", Err: errors.New("connection refused")}, nil}}
	w := New(client, store)
	if err := w.Write(context.Background(), db.ReviewRun{ID: 55, MergeRequestID: 99, Status: "running"}, []db.ReviewFinding{{ID: 1, Path: "pkg/file.go", AnchorKind: "new_line", NewLine: sql.NullInt32{Int32: 17, Valid: true}, Title: "Issue one", Confidence: 0.8}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if client.discussionCalls != 3 {
		t.Fatalf("discussion calls = %d, want 3", client.discussionCalls)
	}
	if store.actions["run:55:finding:1:create_discussion"].Status != commentActionStatusSucceeded {
		t.Fatalf("final action status = %q, want succeeded", store.actions["run:55:finding:1:create_discussion"].Status)
	}
}

type fakeDiscussionClient struct {
	requests         []CreateDiscussionRequest
	noteRequests     []CreateNoteRequest
	resolveRequests  []ResolveDiscussionRequest
	discussionErrors []error
	noteErrors       []error
	resolveErrors    []error
	discussionCalls  int
	noteCalls        int
	resolveCalls     int
}

func (f *fakeDiscussionClient) CreateDiscussion(_ context.Context, req CreateDiscussionRequest) (Discussion, error) {
	f.discussionCalls++
	f.requests = append(f.requests, req)
	if len(f.discussionErrors) > 0 {
		err := f.discussionErrors[0]
		f.discussionErrors = f.discussionErrors[1:]
		if err != nil {
			return Discussion{}, err
		}
	}
	return Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) CreateNote(_ context.Context, req CreateNoteRequest) (Discussion, error) {
	f.noteCalls++
	f.noteRequests = append(f.noteRequests, req)
	if len(f.noteErrors) > 0 {
		err := f.noteErrors[0]
		f.noteErrors = f.noteErrors[1:]
		if err != nil {
			return Discussion{}, err
		}
	}
	return Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) ResolveDiscussion(_ context.Context, req ResolveDiscussionRequest) error {
	f.resolveCalls++
	f.resolveRequests = append(f.resolveRequests, req)
	if len(f.resolveErrors) > 0 {
		err := f.resolveErrors[0]
		f.resolveErrors = f.resolveErrors[1:]
		return err
	}
	return nil
}

type fakeStore struct {
	mr                        db.MergeRequest
	version                   db.MrVersion
	actions                   map[string]db.CommentAction
	runUpdates                map[int64]db.UpdateReviewRunStatusParams
	runsByID                  map[int64]db.ReviewRun
	findingsByRun             map[int64][]db.ReviewFinding
	findingsByID              map[int64]db.ReviewFinding
	discussionByID            map[int64]db.GitlabDiscussion
	discussionByFinding       map[string]db.GitlabDiscussion
	resolvedDiscussionUpdates map[int64]bool
	insertedDiscussions       []db.InsertGitlabDiscussionParams
	nextActionID              int64
}

func (f *fakeStore) GetLatestMRVersion(context.Context, int64) (db.MrVersion, error) {
	return f.version, nil
}
func (f *fakeStore) GetMergeRequest(context.Context, int64) (db.MergeRequest, error) {
	return f.mr, nil
}
func (f *fakeStore) GetReviewRun(_ context.Context, id int64) (db.ReviewRun, error) {
	if run, ok := f.runsByID[id]; ok {
		return run, nil
	}
	return db.ReviewRun{}, errors.New("not found")
}
func (f *fakeStore) GetReviewFinding(_ context.Context, id int64) (db.ReviewFinding, error) {
	if finding, ok := f.findingsByID[id]; ok {
		return finding, nil
	}
	return db.ReviewFinding{}, errors.New("not found")
}
func (f *fakeStore) GetGitlabDiscussion(_ context.Context, id int64) (db.GitlabDiscussion, error) {
	for _, discussion := range f.discussionByID {
		if discussion.ID == id {
			return discussion, nil
		}
	}
	return db.GitlabDiscussion{}, errors.New("not found")
}
func (f *fakeStore) ListFindingsByRun(_ context.Context, runID int64) ([]db.ReviewFinding, error) {
	if f.findingsByRun == nil {
		return nil, nil
	}
	return f.findingsByRun[runID], nil
}
func (f *fakeStore) GetCommentActionByIdempotencyKey(_ context.Context, key string) (db.CommentAction, error) {
	if f.actions == nil {
		return db.CommentAction{}, errors.New("not found")
	}
	action, ok := f.actions[key]
	if !ok {
		return db.CommentAction{}, errors.New("not found")
	}
	return action, nil
}
func (f *fakeStore) InsertCommentAction(_ context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	if f.actions == nil {
		f.actions = map[string]db.CommentAction{}
	}
	f.nextActionID++
	f.actions[arg.IdempotencyKey] = db.CommentAction{ID: f.nextActionID, IdempotencyKey: arg.IdempotencyKey, Status: arg.Status, ActionType: arg.ActionType}
	return fakeResult(f.nextActionID), nil
}
func (f *fakeStore) UpdateCommentActionStatus(_ context.Context, arg db.UpdateCommentActionStatusParams) error {
	for key, action := range f.actions {
		if action.ID == arg.ID {
			action.Status = arg.Status
			action.ErrorCode = arg.ErrorCode
			action.ErrorDetail = arg.ErrorDetail
			action.RetryCount = arg.RetryCount
			f.actions[key] = action
			return nil
		}
	}
	return nil
}
func (f *fakeStore) InsertGitlabDiscussion(_ context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	if f.discussionByFinding == nil {
		f.discussionByFinding = map[string]db.GitlabDiscussion{}
	}
	if f.discussionByID == nil {
		f.discussionByID = map[int64]db.GitlabDiscussion{}
	}
	f.insertedDiscussions = append(f.insertedDiscussions, arg)
	discussion := db.GitlabDiscussion{ID: int64(len(f.insertedDiscussions)), ReviewFindingID: arg.ReviewFindingID, MergeRequestID: arg.MergeRequestID, GitlabDiscussionID: arg.GitlabDiscussionID, DiscussionType: arg.DiscussionType, Resolved: arg.Resolved}
	f.discussionByFinding[fmt.Sprintf("%d:%d", arg.MergeRequestID, arg.ReviewFindingID)] = discussion
	f.discussionByID[arg.ReviewFindingID] = discussion
	return fakeResult(int64(len(f.insertedDiscussions))), nil
}

func (f *fakeStore) UpdateFindingDiscussionID(_ context.Context, arg db.UpdateFindingDiscussionIDParams) error {
	if f.findingsByID == nil {
		f.findingsByID = map[int64]db.ReviewFinding{}
	}
	finding := f.findingsByID[arg.ID]
	finding.ID = arg.ID
	finding.GitlabDiscussionID = arg.GitlabDiscussionID
	f.findingsByID[arg.ID] = finding
	return nil
}

func (f *fakeStore) UpdateGitlabDiscussionSupersededBy(_ context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error {
	for findingID, discussion := range f.discussionByID {
		if discussion.ID == arg.ID {
			discussion.SupersededByDiscussionID = arg.SupersededByDiscussionID
			f.discussionByID[findingID] = discussion
		}
	}
	for key, discussion := range f.discussionByFinding {
		if discussion.ID == arg.ID {
			discussion.SupersededByDiscussionID = arg.SupersededByDiscussionID
			f.discussionByFinding[key] = discussion
		}
	}
	return nil
}

func (f *fakeStore) GetGitlabDiscussionByFinding(_ context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	if discussion, ok := f.discussionByID[reviewFindingID]; ok {
		return discussion, nil
	}
	return db.GitlabDiscussion{}, errors.New("not found")
}

func (f *fakeStore) GetGitlabDiscussionByMergeRequestAndFinding(_ context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	if discussion, ok := f.discussionByFinding[fmt.Sprintf("%d:%d", arg.MergeRequestID, arg.ReviewFindingID)]; ok {
		return discussion, nil
	}
	return db.GitlabDiscussion{}, errors.New("not found")
}

func (f *fakeStore) UpdateGitlabDiscussionResolved(_ context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error {
	if f.resolvedDiscussionUpdates == nil {
		f.resolvedDiscussionUpdates = map[int64]bool{}
	}
	f.resolvedDiscussionUpdates[arg.ID] = arg.Resolved
	for findingID, discussion := range f.discussionByID {
		if discussion.ID == arg.ID {
			discussion.Resolved = arg.Resolved
			f.discussionByID[findingID] = discussion
		}
	}
	for key, discussion := range f.discussionByFinding {
		if discussion.ID == arg.ID {
			discussion.Resolved = arg.Resolved
			f.discussionByFinding[key] = discussion
		}
	}
	return nil
}

func (f *fakeStore) MarkReviewRunFailedIfRunning(_ context.Context, arg db.MarkReviewRunFailedParams) (bool, error) {
	if f.runUpdates == nil {
		f.runUpdates = map[int64]db.UpdateReviewRunStatusParams{}
	}
	f.runUpdates[arg.ID] = db.UpdateReviewRunStatusParams{Status: "failed", ErrorCode: arg.ErrorCode, ErrorDetail: arg.ErrorDetail, ID: arg.ID}
	if f.runsByID == nil {
		f.runsByID = map[int64]db.ReviewRun{}
	}
	run := f.runsByID[arg.ID]
	run.ID = arg.ID
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "running"
	}
	if run.Status != "running" {
		return false, nil
	}
	run.Status = "failed"
	run.ErrorCode = arg.ErrorCode
	run.ErrorDetail = arg.ErrorDetail
	run.RetryCount = arg.RetryCount
	f.runsByID[arg.ID] = run
	return true, nil
}

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fakeResult) RowsAffected() (int64, error) { return 1, nil }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

type retryableHTTPError int

func (e retryableHTTPError) Error() string   { return fmt.Sprintf("http %d", int(e)) }
func (e retryableHTTPError) StatusCode() int { return int(e) }
