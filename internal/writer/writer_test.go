package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
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

type fakeDiscussionClient struct{ requests []CreateDiscussionRequest }

func (f *fakeDiscussionClient) CreateDiscussion(_ context.Context, req CreateDiscussionRequest) (Discussion, error) {
	f.requests = append(f.requests, req)
	return Discussion{ID: req.IdempotencyKey}, nil
}

type fakeStore struct {
	mr                  db.MergeRequest
	version             db.MrVersion
	actions             map[string]db.CommentAction
	insertedDiscussions []db.InsertGitlabDiscussionParams
	nextActionID        int64
}

func (f *fakeStore) GetLatestMRVersion(context.Context, int64) (db.MrVersion, error) {
	return f.version, nil
}
func (f *fakeStore) GetMergeRequest(context.Context, int64) (db.MergeRequest, error) {
	return f.mr, nil
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
	f.actions[arg.IdempotencyKey] = db.CommentAction{ID: f.nextActionID, IdempotencyKey: arg.IdempotencyKey, Status: arg.Status}
	return fakeResult(f.nextActionID), nil
}
func (f *fakeStore) UpdateCommentActionStatus(_ context.Context, arg db.UpdateCommentActionStatusParams) error {
	for key, action := range f.actions {
		if action.ID == arg.ID {
			action.Status = arg.Status
			f.actions[key] = action
			return nil
		}
	}
	return nil
}
func (f *fakeStore) InsertGitlabDiscussion(_ context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	f.insertedDiscussions = append(f.insertedDiscussions, arg)
	return fakeResult(int64(len(f.insertedDiscussions))), nil
}

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fakeResult) RowsAffected() (int64, error) { return 1, nil }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
