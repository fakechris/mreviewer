package reviewpack

import (
	"context"
	"strings"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

func TestDefaultPacksExposeCapabilityContracts(t *testing.T) {
	packs := DefaultPacks()
	if len(packs) != 3 {
		t.Fatalf("DefaultPacks len = %d, want 3", len(packs))
	}

	var securityFound bool
	for _, pack := range packs {
		contract := pack.Contract()
		if contract.ID == "" {
			t.Fatalf("empty contract id")
		}
		if len(contract.FocusAreas) == 0 {
			t.Fatalf("pack %q missing focus areas", contract.ID)
		}
		if contract.OutputSchema == "" {
			t.Fatalf("pack %q missing output schema", contract.ID)
		}
		if contract.ID == "security" {
			securityFound = true
			if len(contract.Standards) == 0 {
				t.Fatalf("security standards should not be empty")
			}
		}
	}

	if !securityFound {
		t.Fatal("security pack not found")
	}
}

type fakeDynamicProvider struct {
	name         string
	response     llm.ProviderResponse
	systemPrompt string
	request      ctxpkg.ReviewRequest
}

func (f *fakeDynamicProvider) Review(_ context.Context, request ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	f.request = request
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	f.request = request
	return map[string]any{"review_run_id": request.ReviewRunID}
}

func (f *fakeDynamicProvider) ReviewWithSystemPrompt(_ context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	f.request = request
	f.systemPrompt = systemPrompt
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	f.request = request
	f.systemPrompt = systemPrompt
	return map[string]any{"review_run_id": request.ReviewRunID, "system_prompt": systemPrompt}
}

func TestLegacyProviderRunnerUsesCapabilityPromptAndBridgesArtifact(t *testing.T) {
	provider := &fakeDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				Summary: "Security reviewer found one issue.",
				Findings: []llm.ReviewFinding{{
					Category:     "security.sql-injection",
					Severity:     "high",
					Confidence:   0.93,
					Title:        "Raw SQL uses untrusted input",
					BodyMarkdown: "User input reaches a raw SQL string.",
					Path:         "internal/db/query.go",
					AnchorKind:   "new_line",
					NewLine:      int32Ptr(44),
					CanonicalKey: "security.sql-injection|query.go|44",
				}},
			},
		},
	}

	runner := NewLegacyProviderRunner(DefaultPacks()[0].Contract(), provider)
	artifact, err := runner.Run(context.Background(), core.ReviewInput{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		Request: ctxpkg.ReviewRequest{
			ReviewRunID: "run-23",
			Project:     ctxpkg.ProjectContext{ProjectID: 77, FullPath: "group/repo"},
			MergeRequest: ctxpkg.MergeRequestContext{
				IID:   23,
				Title: "Fix auth boundary",
			},
		},
	}, core.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(strings.ToLower(provider.systemPrompt), "security") {
		t.Fatalf("system prompt = %q, want security context", provider.systemPrompt)
	}
	if !strings.Contains(strings.ToLower(provider.systemPrompt), "owasp") {
		t.Fatalf("system prompt = %q, want owasp", provider.systemPrompt)
	}
	if !strings.Contains(strings.ToLower(provider.systemPrompt), "trust boundaries") {
		t.Fatalf("system prompt = %q, want focus area", provider.systemPrompt)
	}
	if artifact.ReviewerID != "security" {
		t.Fatalf("reviewer id = %q", artifact.ReviewerID)
	}
	if artifact.Summary != "Security reviewer found one issue." {
		t.Fatalf("summary = %q", artifact.Summary)
	}
	if len(artifact.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(artifact.Findings))
	}
	if artifact.Findings[0].Title != "Raw SQL uses untrusted input" {
		t.Fatalf("finding title = %q", artifact.Findings[0].Title)
	}
}

func TestLegacyProviderRunnerUsesRouteOverrideResolver(t *testing.T) {
	defaultProvider := &fakeDynamicProvider{name: "default"}
	secondaryProvider := &fakeDynamicProvider{name: "secondary"}
	runner := NewLegacyResolverRunner(DefaultPacks()[0].Contract(), func(route string) llm.Provider {
		if route == "secondary" {
			return secondaryProvider
		}
		return defaultProvider
	})

	_, err := runner.Run(context.Background(), core.ReviewInput{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		Request:         ctxpkg.ReviewRequest{ReviewRunID: "run-23"},
		EffectivePolicy: rulesEffectivePolicy("default"),
	}, core.RunOptions{RouteOverride: "secondary"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if secondaryProvider.request.ReviewRunID != "run-23" {
		t.Fatalf("secondary provider was not used")
	}
	if defaultProvider.request.ReviewRunID != "" {
		t.Fatalf("default provider should not have been used")
	}
}

func TestLegacyProviderRunnerSkipsWhenPackNotSelected(t *testing.T) {
	provider := &fakeDynamicProvider{name: "default"}
	runner := NewLegacyProviderRunner(DefaultPacks()[0].Contract(), provider)

	artifact, err := runner.Run(context.Background(), core.ReviewInput{
		Target:  core.ReviewTarget{Platform: core.PlatformGitLab},
		Request: ctxpkg.ReviewRequest{ReviewRunID: "run-23"},
	}, core.RunOptions{ReviewerPacks: []string{"database"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if artifact.ReviewerID != "" {
		t.Fatalf("reviewer id = %q, want empty skipped artifact", artifact.ReviewerID)
	}
	if provider.request.ReviewRunID != "" {
		t.Fatalf("provider should not be called for skipped pack")
	}
}

func rulesEffectivePolicy(route string) coreRunEffectivePolicy {
	return rules.EffectivePolicy{ProviderRoute: route}
}

type coreRunEffectivePolicy = rules.EffectivePolicy

func int32Ptr(v int32) *int32 { return &v }
