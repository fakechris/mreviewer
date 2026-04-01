package reviewcore

import (
	"context"
	"encoding/json"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
)

type fakeDynamicProvider struct {
	request      ctxpkg.ReviewRequest
	systemPrompt string
	response     llm.ProviderResponse
}

func (f *fakeDynamicProvider) Review(_ context.Context, request ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	f.request = request
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"review_run_id": request.ReviewRunID}
}

func (f *fakeDynamicProvider) ReviewWithSystemPrompt(_ context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	f.request = request
	f.systemPrompt = systemPrompt
	return f.response, nil
}

func (f *fakeDynamicProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{"review_run_id": request.ReviewRunID, "system_prompt": systemPrompt}
}

func TestLegacyProviderPackRunnerUsesPackPromptAndBridgesArtifact(t *testing.T) {
	pack, ok := reviewpack.Lookup("security")
	if !ok {
		t.Fatal("expected security pack")
	}

	runner := NewLegacyProviderPackRunner(&fakeDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				Summary: "Security issues found",
				Status:  "requested_changes",
				Findings: []llm.ReviewFinding{
					{
						Category:     "security.sql_injection",
						Severity:     "high",
						Confidence:   0.92,
						Title:        "Unsafe tenant lookup",
						BodyMarkdown: "User-controlled tenant id reaches string-built SQL.",
						Path:         "repo/query.go",
						NewLine:      int32ptr(91),
					},
				},
			},
		},
	})

	artifact, err := runner.Run(context.Background(), ReviewInput{
		Target:       ReviewTarget{Repository: "group/proj"},
		SystemPrompt: "Base review instructions",
		RequestPayload: mustJSON(ctxpkg.ReviewRequest{
			ReviewRunID: "rr_123",
		}),
	}, pack)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if artifact.ReviewerID != "security" {
		t.Fatalf("expected reviewer id security, got %q", artifact.ReviewerID)
	}
	if len(artifact.Findings) != 1 {
		t.Fatalf("expected bridged findings, got %#v", artifact.Findings)
	}
	if artifact.Findings[0].Severity != "high" {
		t.Fatalf("expected severity high, got %q", artifact.Findings[0].Severity)
	}
}

func TestLegacyProviderPackRunnerAppliesPackConfidenceGate(t *testing.T) {
	pack, ok := reviewpack.Lookup("security")
	if !ok {
		t.Fatal("expected security pack")
	}

	runner := NewLegacyProviderPackRunner(&fakeDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				Summary: "Security issues found",
				Status:  "requested_changes",
				Findings: []llm.ReviewFinding{
					{
						Category:     "security.sql_injection",
						Severity:     "high",
						Confidence:   0.40,
						Title:        "Low-confidence issue",
						BodyMarkdown: "Weak signal only.",
						Path:         "repo/query.go",
						NewLine:      int32ptr(91),
					},
				},
			},
		},
	})

	artifact, err := runner.Run(context.Background(), ReviewInput{
		RequestPayload: mustJSON(ctxpkg.ReviewRequest{ReviewRunID: "rr_123"}),
	}, pack)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(artifact.Findings) != 0 {
		t.Fatalf("expected low-confidence findings to be filtered, got %#v", artifact.Findings)
	}
}

func TestRouteAwareLegacyProviderPackRunnerUsesMetadataRoute(t *testing.T) {
	primary := &fakeDynamicProvider{
		response: llm.ProviderResponse{Result: llm.ReviewResult{Summary: "primary"}},
	}
	secondary := &fakeDynamicProvider{
		response: llm.ProviderResponse{Result: llm.ReviewResult{Summary: "secondary"}},
	}
	runner := NewRouteAwareLegacyProviderPackRunner(func(route string) llm.Provider {
		if route == "secondary-route" {
			return secondary
		}
		return primary
	})
	pack, _ := reviewpack.Lookup("architecture")

	artifact, err := runner.Run(context.Background(), ReviewInput{
		Metadata: map[string]string{"provider_route": "secondary-route"},
		RequestPayload: mustJSON(ctxpkg.ReviewRequest{
			ReviewRunID: "rr_999",
		}),
	}, pack)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if artifact.Summary != "secondary" {
		t.Fatalf("expected route-aware runner to use secondary provider, got %q", artifact.Summary)
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
