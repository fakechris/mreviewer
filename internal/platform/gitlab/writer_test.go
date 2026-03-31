package gitlab

import (
	"encoding/json"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestWriterBuildRequestsCreatesSummaryAndInlineCandidates(t *testing.T) {
	writer := NewWriter()
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "Overall summary"},
			{
				Kind:     "finding",
				Title:    "Potential SQL injection",
				Body:     "User-controlled input reaches raw SQL.",
				Severity: "high",
				Location: core.CanonicalLocation{
					Path:      "internal/db/query.go",
					Side:      core.DiffSideNew,
					StartLine: 44,
					EndLine:   44,
				},
			},
		},
	}

	requests, err := writer.BuildRequests(bundle)
	if err != nil {
		t.Fatalf("BuildRequests: %v", err)
	}
	if len(requests.Notes) != 1 {
		t.Fatalf("notes len = %d, want 1", len(requests.Notes))
	}
	if len(requests.Discussions) != 1 {
		t.Fatalf("discussions len = %d, want 1", len(requests.Discussions))
	}
	if requests.Discussions[0].MergeRequestIID != 23 {
		t.Fatalf("discussion MR IID = %d, want 23", requests.Discussions[0].MergeRequestIID)
	}
	if requests.Discussions[0].Position.NewPath != "internal/db/query.go" {
		t.Fatalf("new path = %q", requests.Discussions[0].Position.NewPath)
	}
}

func TestWriterBuildRequestsUsesPlatformMetadataForOldLineAnchors(t *testing.T) {
	writer := NewWriter()
	metadata, err := json.Marshal(map[string]any{
		"base_sha":  "base-sha",
		"start_sha": "start-sha",
		"head_sha":  "head-sha",
		"old_path":  "pkg/old_name.go",
		"new_path":  "pkg/new_name.go",
		"old_line":  17,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{{
			Kind:     "finding",
			Title:    "Removed unsafe check",
			Body:     "A deleted guard can cause bad behavior.",
			Severity: "high",
			Location: core.CanonicalLocation{
				Path:             "pkg/new_name.go",
				Side:             core.DiffSideOld,
				StartLine:        17,
				EndLine:          17,
				PlatformMetadata: metadata,
			},
		}},
	}

	requests, err := writer.BuildRequests(bundle)
	if err != nil {
		t.Fatalf("BuildRequests: %v", err)
	}
	if len(requests.Discussions) != 1 {
		t.Fatalf("discussions len = %d, want 1", len(requests.Discussions))
	}
	position := requests.Discussions[0].Position
	if position.BaseSHA != "base-sha" || position.StartSHA != "start-sha" || position.HeadSHA != "head-sha" {
		t.Fatalf("position shas = %+v, want base/start/head metadata", position)
	}
	if position.OldPath != "pkg/old_name.go" || position.NewPath != "pkg/new_name.go" {
		t.Fatalf("position paths = old:%q new:%q", position.OldPath, position.NewPath)
	}
	if position.OldLine == nil || *position.OldLine != 17 {
		t.Fatalf("position old_line = %+v, want 17", position.OldLine)
	}
	if position.NewLine != nil {
		t.Fatalf("position new_line = %+v, want nil", position.NewLine)
	}
}

func TestWriterBuildRequestsUsesPlatformMetadataLineRange(t *testing.T) {
	writer := NewWriter()
	metadata, err := json.Marshal(map[string]any{
		"base_sha":  "base-sha",
		"start_sha": "start-sha",
		"head_sha":  "head-sha",
		"old_path":  "pkg/file.go",
		"new_path":  "pkg/file.go",
		"line_range": map[string]any{
			"start": map[string]any{
				"line_code": "pkg/file.go_10_10",
				"type":      "new",
				"new_line":  10,
			},
			"end": map[string]any{
				"line_code": "pkg/file.go_0_12",
				"type":      "new",
				"new_line":  12,
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{{
			Kind:     "finding",
			Title:    "Range issue",
			Body:     "This issue spans multiple lines.",
			Severity: "medium",
			Location: core.CanonicalLocation{
				Path:             "pkg/file.go",
				Side:             core.DiffSideNew,
				StartLine:        10,
				EndLine:          12,
				PlatformMetadata: metadata,
			},
		}},
	}

	requests, err := writer.BuildRequests(bundle)
	if err != nil {
		t.Fatalf("BuildRequests: %v", err)
	}
	position := requests.Discussions[0].Position
	if position.LineRange == nil {
		t.Fatal("position line_range = nil, want populated")
	}
	want := &reviewcomment.LineRange{
		Start: reviewcomment.RangeLine{LineCode: "pkg/file.go_10_10", LineType: "new", NewLine: int32Ptr(10)},
		End:   reviewcomment.RangeLine{LineCode: "pkg/file.go_0_12", LineType: "new", NewLine: int32Ptr(12)},
	}
	if got := position.LineRange; got.Start.LineCode != want.Start.LineCode || got.End.LineCode != want.End.LineCode {
		t.Fatalf("line_range = %+v, want %+v", got, want)
	}
	if position.NewLine == nil || *position.NewLine != 12 {
		t.Fatalf("position new_line = %+v, want 12", position.NewLine)
	}
}
