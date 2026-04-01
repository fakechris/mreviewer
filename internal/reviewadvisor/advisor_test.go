package reviewadvisor

import (
	"context"
	"encoding/json"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeDynamicProvider struct {
	systemPrompt string
	response     llm.ProviderResponse
}

func (f *fakeDynamicProvider) Review(_ context.Context, request ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"review_run_id": request.ReviewRunID}
}

func (f *fakeDynamicProvider) ReviewWithSystemPrompt(_ context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	f.systemPrompt = systemPrompt
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{"review_run_id": request.ReviewRunID, "system_prompt": systemPrompt}
}

func TestAdvisorUsesRouteResolvedProviderAndReturnsAdvisorArtifact(t *testing.T) {
	provider := &fakeDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				Summary: "Advisor agrees with the council.",
				Status:  "comment_only",
			},
		},
	}
	advisor := New(func(route string) llm.Provider {
		if route == "openai-gpt-5-4" {
			return provider
		}
		return nil
	})

	artifact, err := advisor.Advise(context.Background(), reviewcore.ReviewInput{
		SystemPrompt:   "Base review prompt",
		RequestPayload: mustJSON(ctxpkg.ReviewRequest{ReviewRunID: "rr_1"}),
	}, reviewcore.ReviewBundle{
		JudgeVerdict: reviewcore.VerdictRequestedChanges,
		JudgeSummary: "The council found a security regression.",
		Artifacts: []reviewcore.ReviewerArtifact{
			{ReviewerID: "security", ReviewerType: "pack", Summary: "Found auth bypass"},
		},
	}, "openai-gpt-5-4")
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}
	if artifact == nil {
		t.Fatal("expected advisor artifact")
	}
	if artifact.ReviewerType != "advisor" {
		t.Fatalf("expected advisor reviewer type, got %q", artifact.ReviewerType)
	}
	if provider.systemPrompt == "" {
		t.Fatal("expected advisor to build dynamic prompt")
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
