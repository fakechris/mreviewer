package reviewcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
)

type LegacyProviderPackRunner struct {
	provider llm.Provider
	resolve  func(string) llm.Provider
}

func NewLegacyProviderPackRunner(provider llm.Provider) *LegacyProviderPackRunner {
	return &LegacyProviderPackRunner{provider: provider}
}

func NewRouteAwareLegacyProviderPackRunner(resolve func(string) llm.Provider) *LegacyProviderPackRunner {
	return &LegacyProviderPackRunner{resolve: resolve}
}

func (r *LegacyProviderPackRunner) Run(ctx context.Context, input ReviewInput, pack reviewpack.CapabilityPack) (ReviewerArtifact, error) {
	if len(input.RequestPayload) == 0 {
		return ReviewerArtifact{}, fmt.Errorf("legacy provider pack runner: request payload is required")
	}

	var request ctxpkg.ReviewRequest
	if err := json.Unmarshal(input.RequestPayload, &request); err != nil {
		return ReviewerArtifact{}, fmt.Errorf("legacy provider pack runner: decode request payload: %w", err)
	}

	systemPrompt := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(input.SystemPrompt),
		strings.TrimSpace(pack.SystemPrompt()),
	}, "\n\n"))
	provider := r.provider
	if r != nil && r.resolve != nil {
		provider = r.resolve(resolveRoute(input.Metadata))
	}
	if provider == nil {
		return ReviewerArtifact{}, fmt.Errorf("legacy provider pack runner: provider is required")
	}

	var (
		response llm.ProviderResponse
		err      error
	)
	if dynamic, ok := provider.(llm.DynamicPromptProvider); ok {
		response, err = dynamic.ReviewWithSystemPrompt(ctx, request, systemPrompt)
	} else {
		if systemPrompt != "" {
			return ReviewerArtifact{}, fmt.Errorf("legacy provider pack runner: provider does not support dynamic system prompts")
		}
		response, err = provider.Review(ctx, request)
	}
	if err != nil {
		return ReviewerArtifact{}, err
	}

	artifact := ArtifactFromLegacyResult(pack.ID, response.Result)
	artifact.ReviewerType = "pack"
	if artifact.Summary == "" {
		artifact.Summary = response.Result.Summary
	}
	artifact.Findings = filterPackFindings(pack, artifact.Findings)
	return artifact, nil
}

func resolveRoute(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata["provider_route"])
}

func filterPackFindings(pack reviewpack.CapabilityPack, findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	filtered := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		if pack.ConfidenceGate > 0 && finding.Confidence > 0 && finding.Confidence < pack.ConfidenceGate {
			continue
		}
		if isExcludedFinding(pack, finding) {
			continue
		}
		filtered = append(filtered, finding)
	}
	return filtered
}

func isExcludedFinding(pack reviewpack.CapabilityPack, finding Finding) bool {
	if len(pack.HardExclusions) == 0 {
		return false
	}
	text := strings.ToLower(strings.Join(append([]string{
		finding.Title,
		finding.Category,
		finding.Claim,
		finding.Recommendation,
	}, finding.Evidence...), "\n"))
	for _, exclusion := range pack.HardExclusions {
		exclusion = strings.ToLower(strings.TrimSpace(exclusion))
		if exclusion == "" {
			continue
		}
		if strings.Contains(text, exclusion) {
			return true
		}
	}
	return false
}
