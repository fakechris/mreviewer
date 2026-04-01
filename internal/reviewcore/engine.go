package reviewcore

import "context"

type RunOptions struct {
	OutputMode    string
	PublishMode   string
	ReviewerPacks []string
	RouteOverride string
	AdvisorRoute  string
}

type PackRunner interface {
	Run(ctx context.Context, input ReviewInput, opts RunOptions) (ReviewerArtifact, error)
}

type JudgeDecision struct {
	Verdict        string    `json:"verdict,omitempty"`
	MergedFindings []Finding `json:"merged_findings,omitempty"`
	Summary        string    `json:"summary,omitempty"`
}

type Judge interface {
	Decide(artifacts []ReviewerArtifact) JudgeDecision
}

type Engine struct {
	packs []PackRunner
	judge Judge
}

func NewEngine(packs []PackRunner, judge Judge) *Engine {
	return &Engine{packs: packs, judge: judge}
}

func (e *Engine) Run(ctx context.Context, input ReviewInput, opts RunOptions) (ReviewBundle, error) {
	artifacts := make([]ReviewerArtifact, 0, len(e.packs))
	for _, pack := range e.packs {
		artifact, err := pack.Run(ctx, input, opts)
		if err != nil {
			return ReviewBundle{}, err
		}
		if artifact.ReviewerID == "" && artifact.Summary == "" && len(artifact.Findings) == 0 {
			continue
		}
		artifacts = append(artifacts, artifact)
	}

	decision := JudgeDecision{}
	if e.judge != nil {
		decision = e.judge.Decide(artifacts)
	}

	bundle := ReviewBundle{
		Target:            input.Target,
		Artifacts:         artifacts,
		Verdict:           decision.Verdict,
		MarkdownSummary:   decision.Summary,
		JSONSchemaVersion: "v1alpha1",
	}
	if decision.Summary != "" {
		bundle.PublishCandidates = append(bundle.PublishCandidates, PublishCandidate{
			Kind: "summary",
			Body: decision.Summary,
		})
	}
	for _, finding := range decision.MergedFindings {
		bundle.PublishCandidates = append(bundle.PublishCandidates, PublishCandidate{
			Kind:     "finding",
			Title:    finding.Title,
			Body:     finding.Body,
			Severity: finding.Severity,
			Location: finding.Identity.Location,
		})
	}
	return bundle, nil
}
