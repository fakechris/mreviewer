package reviewadvisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type Advisor struct {
	resolve func(string) llm.Provider
}

func New(resolve func(string) llm.Provider) *Advisor {
	return &Advisor{resolve: resolve}
}

func (a *Advisor) Advise(ctx context.Context, input reviewcore.ReviewInput, bundle reviewcore.ReviewBundle, route string) (*reviewcore.ReviewerArtifact, error) {
	if a == nil || a.resolve == nil {
		return nil, fmt.Errorf("review advisor: provider resolver is required")
	}
	if strings.TrimSpace(route) == "" {
		return nil, nil
	}
	provider := a.resolve(route)
	if provider == nil {
		return nil, fmt.Errorf("review advisor: advisor route %q is not configured", route)
	}
	if len(input.RequestPayload) == 0 {
		return nil, fmt.Errorf("review advisor: request payload is required")
	}

	var request ctxpkg.ReviewRequest
	if err := json.Unmarshal(input.RequestPayload, &request); err != nil {
		return nil, fmt.Errorf("review advisor: decode request payload: %w", err)
	}

	systemPrompt := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(input.SystemPrompt),
		buildAdvisorPrompt(bundle),
	}, "\n\n"))

	dynamic, ok := provider.(llm.DynamicPromptProvider)
	if !ok {
		return nil, fmt.Errorf("review advisor: provider does not support dynamic system prompts")
	}
	response, err := dynamic.ReviewWithSystemPrompt(ctx, request, systemPrompt)
	if err != nil {
		return nil, err
	}

	artifact := reviewcore.ArtifactFromLegacyResult("advisor", response.Result)
	artifact.ReviewerType = "advisor"
	if artifact.Summary == "" {
		artifact.Summary = response.Result.Summary
	}
	return &artifact, nil
}

func buildAdvisorPrompt(bundle reviewcore.ReviewBundle) string {
	payload, _ := json.Marshal(struct {
		JudgeVerdict reviewcore.Verdict            `json:"judge_verdict"`
		JudgeSummary string                        `json:"judge_summary,omitempty"`
		Artifacts    []reviewcore.ReviewerArtifact `json:"artifacts,omitempty"`
	}{
		JudgeVerdict: bundle.JudgeVerdict,
		JudgeSummary: bundle.JudgeSummary,
		Artifacts:    bundle.Artifacts,
	})
	return strings.TrimSpace(strings.Join([]string{
		"You are the stronger second-opinion reviewer.",
		"Review the existing council artifacts and judge result. Only add omitted high-confidence findings, or explicitly affirm the current decision if it is sound.",
		"Do not restate weak concerns. Prefer challenging the council only when you have stronger evidence or a materially different verdict.",
		"Advisor input:",
		string(payload),
	}, "\n\n"))
}
