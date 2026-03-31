package github

import (
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestResolveTargetParsesGitHubPullRequestURL(t *testing.T) {
	target, err := ResolveTarget("https://github.com/acme/repo/pull/17")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Platform != core.PlatformGitHub {
		t.Fatalf("platform = %q, want %q", target.Platform, core.PlatformGitHub)
	}
	if target.BaseURL != "https://github.com" {
		t.Fatalf("base_url = %q, want https://github.com", target.BaseURL)
	}
	if target.Repository != "acme/repo" {
		t.Fatalf("repository = %q, want acme/repo", target.Repository)
	}
	if target.ChangeNumber != 17 {
		t.Fatalf("change_number = %d, want 17", target.ChangeNumber)
	}
}

func TestResolveTargetRejectsNonPullRequestURL(t *testing.T) {
	if _, err := ResolveTarget("https://github.com/acme/repo/issues/17"); err == nil {
		t.Fatal("ResolveTarget error = nil, want non-nil")
	}
}
