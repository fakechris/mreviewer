package reviewpack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type Contract struct {
	ID                   string   `json:"id"`
	FocusAreas           []string `json:"focus_areas,omitempty"`
	OutputSchema         string   `json:"output_schema,omitempty"`
	Standards            []string `json:"standards,omitempty"`
	HardExclusions       []string `json:"hard_exclusions,omitempty"`
	ConfidenceGate       float64  `json:"confidence_gate,omitempty"`
	NewIssuesOnly        bool     `json:"new_issues_only,omitempty"`
	ExploitabilityFocus  string   `json:"exploitability_focus,omitempty"`
	Prompt               string   `json:"prompt,omitempty"`
	EvidenceRequirements []string `json:"evidence_requirements,omitempty"`
	Rubric               []string `json:"rubric,omitempty"`
}

type Pack interface {
	Contract() Contract
}

type staticPack struct {
	contract Contract
}

func (p staticPack) Contract() Contract { return p.contract }

func DefaultPacks() []Pack {
	return []Pack{
		staticPack{contract: Contract{
			ID:           "security",
			FocusAreas:   []string{"trust boundaries", "input validation", "authz", "unsafe data access"},
			OutputSchema: "review_finding_v1",
			Standards:    []string{"owasp", "asvs"},
			HardExclusions: []string{
				"historical debt",
				"test-only fixtures",
				"vague best-practice",
			},
			ConfidenceGate:      0.85,
			NewIssuesOnly:       true,
			ExploitabilityFocus: "Prioritize attacker-controlled input, reachable dangerous sinks, broken authorization decisions, exposed secrets, and concrete escalation paths.",
			Prompt:              "Use OWASP and ASVS as your security review framing. Report only newly introduced security issues with enough evidence to justify reviewer trust.",
			EvidenceRequirements: []string{
				"Point to the exact changed path and line or hunk when possible.",
				"Explain the attacker-controlled input, dangerous sink, or broken authorization decision.",
				"State the impact if the issue is exploitable.",
			},
			Rubric: []string{
				"Find authorization, injection, secret handling, deserialization, and trust-boundary regressions.",
				"Prefer concrete exploitability over vague best-practice complaints.",
				"Call out only issues that are materially supported by the diff and surrounding context.",
			},
		}},
		staticPack{contract: Contract{
			ID:           "architecture",
			FocusAreas:   []string{"module boundaries", "control flow", "coupling", "change isolation"},
			OutputSchema: "review_finding_v1",
			Prompt:       "Act like a staff engineer reviewing architectural integrity. Avoid generic refactor requests; report issues with concrete structural evidence.",
		}},
		staticPack{contract: Contract{
			ID:           "database",
			FocusAreas:   []string{"queries", "transactions", "indexes", "data integrity"},
			OutputSchema: "review_finding_v1",
			Prompt:       "Act like a database-focused reviewer. Prefer high-signal issues involving data correctness, operational safety, and migration risk.",
		}},
	}
}

type LegacyProviderRunner struct {
	contract Contract
	resolve  func(string) llm.Provider
}

func NewLegacyProviderRunner(contract Contract, provider llm.Provider) *LegacyProviderRunner {
	return &LegacyProviderRunner{
		contract: contract,
		resolve: func(string) llm.Provider {
			return provider
		},
	}
}

func NewLegacyResolverRunner(contract Contract, resolve func(string) llm.Provider) *LegacyProviderRunner {
	return &LegacyProviderRunner{contract: contract, resolve: resolve}
}

func (r *LegacyProviderRunner) Contract() Contract { return r.contract }

func (r *LegacyProviderRunner) Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewerArtifact, error) {
	if r == nil || r.resolve == nil {
		return core.ReviewerArtifact{}, fmt.Errorf("reviewpack: provider is required")
	}
	if !isPackSelected(r.contract.ID, opts.ReviewerPacks) {
		return core.ReviewerArtifact{}, nil
	}
	route := strings.TrimSpace(opts.RouteOverride)
	if route == "" {
		route = strings.TrimSpace(input.EffectivePolicy.ProviderRoute)
		if route == "" {
			route = strings.TrimSpace(input.Metadata["provider_route"])
		}
	}
	provider := r.resolve(route)
	if provider == nil {
		return core.ReviewerArtifact{}, fmt.Errorf("reviewpack: provider route %q is not configured", route)
	}

	request := input.Request
	if len(input.RequestPayload) > 0 {
		var decoded ctxpkg.ReviewRequest
		if err := json.Unmarshal(input.RequestPayload, &decoded); err == nil {
			request = decoded
		}
	}
	systemPrompt := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(input.SystemPrompt),
		buildCapabilityPrompt(r.contract),
	}, "\n\n"))
	response, err := reviewWithCapabilityPrompt(ctx, provider, request, systemPrompt)
	if err != nil {
		return core.ReviewerArtifact{}, err
	}
	artifact := core.ArtifactFromLegacyResult(input.Target, r.contract.ID, response.Result)
	artifact.ReviewerKind = "pack"
	artifact.Findings = filterFindings(r.contract, artifact.Findings)
	return artifact, nil
}

func reviewWithCapabilityPrompt(ctx context.Context, provider llm.Provider, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	if dynamic, ok := provider.(llm.DynamicPromptProvider); ok {
		return dynamic.ReviewWithSystemPrompt(ctx, request, systemPrompt)
	}
	if strings.TrimSpace(systemPrompt) != "" {
		return llm.ProviderResponse{}, fmt.Errorf("reviewpack: provider does not support dynamic system prompts")
	}
	return provider.Review(ctx, request)
}

func buildCapabilityPrompt(contract Contract) string {
	parts := []string{
		"You are a specialist code reviewer.",
	}
	if contract.ID != "" {
		parts = append(parts, fmt.Sprintf("Reviewer pack: %s.", contract.ID))
	}
	if len(contract.FocusAreas) > 0 {
		parts = append(parts, fmt.Sprintf("Focus areas: %s.", strings.Join(contract.FocusAreas, ", ")))
	}
	if len(contract.Standards) > 0 {
		parts = append(parts, fmt.Sprintf("Standards: %s.", strings.Join(contract.Standards, ", ")))
	}
	if len(contract.Rubric) > 0 {
		parts = append(parts, fmt.Sprintf("Rubric: %s.", strings.Join(contract.Rubric, "; ")))
	}
	if len(contract.EvidenceRequirements) > 0 {
		parts = append(parts, fmt.Sprintf("Evidence requirements: %s.", strings.Join(contract.EvidenceRequirements, "; ")))
	}
	if len(contract.HardExclusions) > 0 {
		parts = append(parts, fmt.Sprintf("Hard exclusions: %s.", strings.Join(contract.HardExclusions, "; ")))
	}
	if contract.NewIssuesOnly {
		parts = append(parts, "Report only newly introduced issues from this diff.")
	}
	if contract.ExploitabilityFocus != "" {
		parts = append(parts, contract.ExploitabilityFocus)
	}
	if contract.Prompt != "" {
		parts = append(parts, contract.Prompt)
	}
	parts = append(parts, "Return only valid JSON matching the provided review schema.")
	return strings.Join(parts, " ")
}

func isPackSelected(packID string, selected []string) bool {
	if len(selected) == 0 {
		return true
	}
	packID = strings.TrimSpace(packID)
	for _, item := range selected {
		if strings.EqualFold(strings.TrimSpace(item), packID) {
			return true
		}
	}
	return false
}

func filterFindings(contract Contract, findings []core.Finding) []core.Finding {
	if len(findings) == 0 {
		return nil
	}
	filtered := make([]core.Finding, 0, len(findings))
	for _, finding := range findings {
		if contract.ConfidenceGate > 0 && finding.Confidence > 0 && finding.Confidence < contract.ConfidenceGate {
			continue
		}
		if isExcludedFinding(contract, finding) {
			continue
		}
		filtered = append(filtered, finding)
	}
	return filtered
}

func isExcludedFinding(contract Contract, finding core.Finding) bool {
	if len(contract.HardExclusions) == 0 {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Category,
		finding.Claim,
		finding.Body,
	}, "\n"))
	for _, exclusion := range contract.HardExclusions {
		exclusion = strings.ToLower(strings.TrimSpace(exclusion))
		if exclusion != "" && strings.Contains(text, exclusion) {
			return true
		}
	}
	return false
}
