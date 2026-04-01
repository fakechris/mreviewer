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
	CategoryAnchors      []string `json:"category_anchors,omitempty"`
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
			ID: "security",
			FocusAreas: []string{
				"trust boundaries",
				"input validation",
				"authentication and authorization",
				"unsafe data access and dangerous sinks",
				"secret exposure and privilege escalation paths",
			},
			CategoryAnchors: []string{
				"authorization bypass",
				"injection",
				"secret exposure",
				"privilege escalation",
				"unsafe deserialization",
			},
			OutputSchema: "review_finding_v1",
			Standards:    []string{"owasp", "asvs"},
			HardExclusions: []string{
				"historical debt",
				"test-only fixtures",
				"vague best-practice",
				"pure dos concern without a concrete exploit path",
				"resource exhaustion speculation without attacker leverage",
				"theoretical race without a changed trust boundary",
				"generic dependency age complaint",
				"frontend framework default escaping concern without a concrete bypass path",
				"reliability",
				"missing import",
				"missing dependency",
				"startup crash",
				"build break",
				"runtime crash",
			},
			ConfidenceGate:      0.85,
			NewIssuesOnly:       true,
			ExploitabilityFocus: "Prioritize attacker-controlled input, reachable dangerous sinks, broken authorization decisions, exposed secrets, concrete escalation paths, and explicit attacker leverage.",
			Prompt:              "Use OWASP and ASVS as your security review framing. First identify whether the diff introduces a new reachable vulnerability, then explain the exploit scenario, impact, and safest remediation. Do not report missing imports, missing dependencies, startup crashes, or generic reliability failures unless they create a concrete attacker leverage or trust-boundary break. Report only newly introduced security issues with enough evidence to justify reviewer trust.",
			EvidenceRequirements: []string{
				"Point to the exact changed path and line or hunk when possible.",
				"Explain the attacker-controlled input, dangerous sink, or broken authorization decision.",
				"State the realistic exploit scenario and impact if the issue is exploitable.",
				"Say what remains uncertain when the impact could be high but exploitability evidence is incomplete.",
			},
			Rubric: []string{
				"Find authorization, injection, secret handling, deserialization, authentication, and trust-boundary regressions.",
				"Prefer concrete exploitability over vague best-practice complaints.",
				"Treat standards like OWASP A01, A03, A04, and ASVS access-control or input-validation controls as review lenses, not as box-checking exercises.",
				"Call out only issues that are materially supported by the diff and surrounding context.",
			},
		}},
		staticPack{contract: Contract{
			ID: "architecture",
			FocusAreas: []string{
				"module and service boundaries",
				"state transitions and error propagation",
				"control flow and side-effect ordering",
				"coupling, ownership, and change isolation",
				"concurrency, retries, and idempotency assumptions",
			},
			OutputSchema: "review_finding_v1",
			HardExclusions: []string{
				"generic refactor request",
				"style preference",
				"naming preference",
				"future maintainability hand-wave",
				"possible duplication without a concrete failure mode",
			},
			ConfidenceGate: 0.78,
			NewIssuesOnly:  true,
			Prompt:         "Act like a staff-plus engineer reviewing architectural integrity. Run two passes mentally: first for blocking risks that could make the changed behavior incorrect or fragile in production, then for medium-severity structural regressions that clearly increase coupling or break ownership boundaries. Avoid generic refactor requests.",
			EvidenceRequirements: []string{
				"Name the concrete state flow, call chain, retry path, or side-effect sequence that makes the issue real.",
				"Point to the changed boundary where ownership, ordering, or invariants become unclear.",
				"Explain the production symptom or maintenance hazard, not just the abstract design principle.",
			},
			Rubric: []string{
				"Prioritize incorrect control flow, broken invariants, hidden side effects, leaky boundaries, and retry/idempotency regressions.",
				"Only report maintainability concerns when they create a realistic path to wrong behavior, fragile changes, or duplicated source of truth.",
				"Prefer one precise structural issue over several generic cleanup comments.",
			},
		}},
		staticPack{contract: Contract{
			ID: "database",
			FocusAreas: []string{
				"schema and migration safety",
				"transaction boundaries and consistency",
				"query correctness and lock behavior",
				"index usage and access patterns",
				"backfills, defaults, nullability, and destructive writes",
				"application-layer data consistency and multi-step persistence flows",
			},
			CategoryAnchors: []string{
				"destructive migration",
				"partial write",
				"read/write mismatch",
				"transaction hole",
				"nullability backfill",
				"destructive update or delete",
			},
			OutputSchema: "review_finding_v1",
			Standards:    []string{"migration-safety", "transactional-correctness"},
			HardExclusions: []string{
				"generic add an index",
				"performance speculation without a concrete query path",
				"orm preference",
				"style-only sql comment",
				"historical schema debt unrelated to the diff",
			},
			ConfidenceGate: 0.72,
			NewIssuesOnly:  true,
			Prompt:         "Act like a database-focused reviewer. Prioritize data correctness, operational safety, and migration risk over stylistic SQL feedback. Review both explicit database changes and application-layer changes that can create partial writes, read/write mismatches, stale persisted state, broken idempotency, or non-transactional multi-step persistence flows. Report only issues that are grounded in the changed query path, migration step, transaction boundary, locking behavior, or persistence semantics.",
			EvidenceRequirements: []string{
				"Point to the exact query, migration step, read/write path, or transaction boundary involved.",
				"Explain the concrete failure mode: bad data, partial write, lock contention, deadlock risk, failed deploy, or backwards-incompatible migration.",
				"State why the changed code introduces the risk now, rather than describing long-standing database debt.",
			},
			Rubric: []string{
				"Find destructive migration hazards, incompatible schema transitions, transaction holes, stale-read/write ordering problems, and missing idempotency around persistence.",
				"Flag indexing issues only when the changed access path makes the degradation or lock behavior plausible.",
				"Prefer correctness and operational safety findings over generic performance advice.",
			},
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
	if len(contract.CategoryAnchors) > 0 {
		parts = append(parts, fmt.Sprintf("Category anchors: %s.", strings.Join(contract.CategoryAnchors, ", ")))
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
	if shouldBypassExclusions(contract, finding) {
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

func shouldBypassExclusions(contract Contract, finding core.Finding) bool {
	if contract.ID != "security" {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		finding.Category,
		finding.Title,
		finding.Claim,
		finding.Body,
	}, "\n"))
	for _, anchor := range contract.CategoryAnchors {
		anchor = strings.ToLower(strings.TrimSpace(anchor))
		if anchor != "" && strings.Contains(text, anchor) {
			return true
		}
	}
	for _, marker := range []string{
		"attacker",
		"authorization",
		"auth bypass",
		"tenant check",
		"privilege escalation",
		"secret",
		"injection",
		"exploit",
		"trust boundary",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
