package reviewpack

import (
	"context"
	"fmt"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type Contract struct {
	ID           string   `json:"id"`
	FocusAreas   []string `json:"focus_areas,omitempty"`
	OutputSchema string   `json:"output_schema,omitempty"`
	Standards    []string `json:"standards,omitempty"`
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
		}},
		staticPack{contract: Contract{
			ID:           "architecture",
			FocusAreas:   []string{"module boundaries", "control flow", "coupling", "change isolation"},
			OutputSchema: "review_finding_v1",
		}},
		staticPack{contract: Contract{
			ID:           "database",
			FocusAreas:   []string{"queries", "transactions", "indexes", "data integrity"},
			OutputSchema: "review_finding_v1",
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
	}
	provider := r.resolve(route)
	if provider == nil {
		return core.ReviewerArtifact{}, fmt.Errorf("reviewpack: provider route %q is not configured", route)
	}

	systemPrompt := buildCapabilityPrompt(r.contract)
	response, err := reviewWithCapabilityPrompt(ctx, provider, input.Request, systemPrompt)
	if err != nil {
		return core.ReviewerArtifact{}, err
	}
	return core.ArtifactFromLegacyResult(input.Target, r.contract.ID, response.Result), nil
}

func reviewWithCapabilityPrompt(ctx context.Context, provider llm.Provider, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	if dynamic, ok := provider.(llm.DynamicPromptProvider); ok {
		return dynamic.ReviewWithSystemPrompt(ctx, request, systemPrompt)
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
