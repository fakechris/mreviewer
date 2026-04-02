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

	found := map[string]Contract{}
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
		if len(contract.Rubric) == 0 {
			t.Fatalf("pack %q missing rubric", contract.ID)
		}
		if len(contract.EvidenceRequirements) == 0 {
			t.Fatalf("pack %q missing evidence requirements", contract.ID)
		}
		if !contract.NewIssuesOnly {
			t.Fatalf("pack %q should review only new issues", contract.ID)
		}
		found[contract.ID] = contract
	}

	security, ok := found["security"]
	if !ok {
		t.Fatal("security pack not found")
	}
	if len(security.Standards) == 0 {
		t.Fatalf("security standards should not be empty")
	}
	if security.ConfidenceGate < 0.8 {
		t.Fatalf("security confidence gate = %v, want >= 0.8", security.ConfidenceGate)
	}

	architecture, ok := found["architecture"]
	if !ok {
		t.Fatal("architecture pack not found")
	}
	if architecture.ConfidenceGate == 0 {
		t.Fatalf("architecture confidence gate should not be empty")
	}
	if len(architecture.HardExclusions) == 0 {
		t.Fatalf("architecture hard exclusions should not be empty")
	}

	database, ok := found["database"]
	if !ok {
		t.Fatal("database pack not found")
	}
	if len(database.Standards) == 0 {
		t.Fatalf("database standards should not be empty")
	}
	if len(database.HardExclusions) == 0 {
		t.Fatalf("database hard exclusions should not be empty")
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
	if !strings.Contains(strings.ToLower(provider.systemPrompt), "exploit scenario") {
		t.Fatalf("system prompt = %q, want exploit scenario guidance", provider.systemPrompt)
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

type fakeStaticProvider struct {
	response llm.ProviderResponse
	request  ctxpkg.ReviewRequest
}

func (f *fakeStaticProvider) Review(_ context.Context, request ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	f.request = request
	return f.response, nil
}

func (f *fakeStaticProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	f.request = request
	return map[string]any{"review_run_id": request.ReviewRunID}
}

func TestLegacyProviderRunnerFailsWhenProviderCannotAcceptCapabilityPrompt(t *testing.T) {
	provider := &fakeStaticProvider{}
	runner := NewLegacyProviderRunner(DefaultPacks()[0].Contract(), provider)

	_, err := runner.Run(context.Background(), core.ReviewInput{
		Target:  core.ReviewTarget{Platform: core.PlatformGitLab},
		Request: ctxpkg.ReviewRequest{ReviewRunID: "run-23"},
	}, core.RunOptions{})
	if err == nil {
		t.Fatal("Run error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dynamic system prompts") {
		t.Fatalf("Run error = %v, want dynamic system prompt failure", err)
	}
	if provider.request.ReviewRunID != "" {
		t.Fatalf("provider should not be called when system prompt is unsupported")
	}
}

func TestBuildCapabilityPromptIncludesArchitectureAndDatabaseDiscipline(t *testing.T) {
	packs := DefaultPacks()
	var securityPrompt, architecturePrompt, databasePrompt string
	for _, pack := range packs {
		switch contract := pack.Contract(); contract.ID {
		case "security":
			securityPrompt = strings.ToLower(buildCapabilityPrompt(contract))
		case "architecture":
			architecturePrompt = strings.ToLower(buildCapabilityPrompt(contract))
		case "database":
			databasePrompt = strings.ToLower(buildCapabilityPrompt(contract))
		}
	}
	if !strings.Contains(securityPrompt, "attacker leverage") {
		t.Fatalf("security prompt = %q, want attacker leverage phrasing", securityPrompt)
	}
	if !strings.Contains(securityPrompt, "missing import") {
		t.Fatalf("security prompt = %q, want non-security drift exclusion", securityPrompt)
	}
	if !strings.Contains(securityPrompt, "authorization bypass") {
		t.Fatalf("security prompt = %q, want category anchors", securityPrompt)
	}
	if !strings.Contains(architecturePrompt, "staff-plus engineer") {
		t.Fatalf("architecture prompt = %q, want staff-plus framing", architecturePrompt)
	}
	if !strings.Contains(architecturePrompt, "two passes") {
		t.Fatalf("architecture prompt = %q, want two-pass review guidance", architecturePrompt)
	}
	if !strings.Contains(architecturePrompt, "state flow") {
		t.Fatalf("architecture prompt = %q, want state-flow evidence requirement", architecturePrompt)
	}
	if !strings.Contains(databasePrompt, "migration safety") {
		t.Fatalf("database prompt = %q, want migration safety focus", databasePrompt)
	}
	if !strings.Contains(databasePrompt, "transaction boundary") {
		t.Fatalf("database prompt = %q, want transaction boundary evidence", databasePrompt)
	}
	if !strings.Contains(databasePrompt, "partial write") {
		t.Fatalf("database prompt = %q, want partial-write anchor", databasePrompt)
	}
	if !strings.Contains(databasePrompt, "read/write mismatch") {
		t.Fatalf("database prompt = %q, want read/write mismatch anchor", databasePrompt)
	}
	if !strings.Contains(databasePrompt, "generic add an index") {
		t.Fatalf("database prompt = %q, want hard exclusion guidance", databasePrompt)
	}
}

func TestSecurityContractExposesExclusionBypassKeywords(t *testing.T) {
	contract := securityContract(t)
	if len(contract.ExclusionBypassKeywords) == 0 {
		t.Fatal("security contract exclusion bypass keywords should not be empty")
	}
	if !shouldBypassExclusions(contract, core.Finding{
		Title: "Attacker can bypass authorization after tenant check removal",
	}) {
		t.Fatal("security contract should bypass exclusions when configured keywords match")
	}
}

func TestShouldBypassExclusionsUsesConfiguredKeywords(t *testing.T) {
	finding := core.Finding{Title: "Attacker leverage exists through a concrete exploit path"}
	if shouldBypassExclusions(Contract{}, finding) {
		t.Fatal("empty contract should not bypass exclusions")
	}
	if !shouldBypassExclusions(Contract{ExclusionBypassKeywords: []string{"attacker leverage"}}, finding) {
		t.Fatal("configured bypass keyword should bypass exclusions")
	}
}

func securityContract(t *testing.T) Contract {
	t.Helper()
	for _, pack := range DefaultPacks() {
		contract := pack.Contract()
		if contract.ID == "security" {
			return contract
		}
	}
	t.Fatal("security contract not found")
	return Contract{}
}

func TestFilterFindingsAppliesConfidenceGateAndHardExclusions(t *testing.T) {
	contract := Contract{
		ID:             "database",
		ConfidenceGate: 0.8,
		HardExclusions: []string{"generic add an index"},
	}
	findings := []core.Finding{
		{Title: "Query path causes duplicate writes", Confidence: 0.92},
		{Title: "Generic add an index recommendation", Confidence: 0.95},
		{Title: "Weak evidence", Confidence: 0.5},
	}
	filtered := filterFindings(contract, findings)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d, want 1", len(filtered))
	}
	if filtered[0].Title != "Query path causes duplicate writes" {
		t.Fatalf("filtered[0].Title = %q", filtered[0].Title)
	}
}

func TestFilterFindingsExcludesSecurityReliabilityDrift(t *testing.T) {
	contract := securityContract(t)
	findings := []core.Finding{
		{
			Category:   "reliability",
			Title:      "Missing secureStorage import causes app startup crash",
			Body:       "The app will fail to start because secureStorage dependency is missing.",
			Confidence: 0.95,
		},
		{
			Category:   "authz",
			Title:      "Tenant check removed from assignment flow",
			Body:       "Attacker can assign cross-tenant resources without authorization.",
			Confidence: 0.95,
		},
	}
	filtered := filterFindings(contract, findings)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d, want 1", len(filtered))
	}
	if filtered[0].Title != "Tenant check removed from assignment flow" {
		t.Fatalf("filtered[0].Title = %q", filtered[0].Title)
	}
}

func TestFilterFindingsKeepsSecurityFindingWithReliabilityWordsWhenSecuritySignalsExist(t *testing.T) {
	contract := securityContract(t)
	findings := []core.Finding{
		{
			Category:   "authorization bypass",
			Title:      "Auth bypass can trigger runtime crash across tenants",
			Body:       "An attacker can call the endpoint without tenant checks and cause a runtime crash in another tenant's workflow.",
			Confidence: 0.95,
		},
	}
	filtered := filterFindings(contract, findings)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d, want 1", len(filtered))
	}
}

func rulesEffectivePolicy(route string) coreRunEffectivePolicy {
	return rules.EffectivePolicy{ProviderRoute: route}
}

type coreRunEffectivePolicy = rules.EffectivePolicy

func int32Ptr(v int32) *int32 { return &v }
