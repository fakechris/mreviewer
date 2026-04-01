package reviewcore

import (
	"context"
	"encoding/json"
	"strings"
)

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
		bundle.PublishCandidates = append(bundle.PublishCandidates, publishCandidateForFinding(input, finding))
	}
	return bundle, nil
}

func publishCandidateForFinding(input ReviewInput, finding Finding) PublishCandidate {
	location := locationWithGitLabVersionMetadata(input, finding.Identity.Location)
	if !locationSupportsInlineDiscussion(location) {
		return PublishCandidate{
			Kind:     "summary",
			Title:    finding.Title,
			Body:     summaryBodyFromFinding(finding.Title, finding.Body),
			Severity: finding.Severity,
			Location: location,
		}
	}
	return PublishCandidate{
		Kind:     "finding",
		Title:    finding.Title,
		Body:     finding.Body,
		Severity: finding.Severity,
		Location: location,
	}
}

func locationWithGitLabVersionMetadata(input ReviewInput, location CanonicalLocation) CanonicalLocation {
	if input.Target.Platform != PlatformGitLab {
		return location
	}
	if strings.TrimSpace(location.Path) == "" {
		return location
	}
	baseSHA := strings.TrimSpace(input.Request.Version.BaseSHA)
	startSHA := strings.TrimSpace(input.Request.Version.StartSHA)
	headSHA := strings.TrimSpace(input.Request.Version.HeadSHA)
	if baseSHA == "" && startSHA == "" && headSHA == "" {
		return location
	}

	metadata := map[string]any{}
	if len(location.PlatformMetadata) > 0 {
		if err := json.Unmarshal(location.PlatformMetadata, &metadata); err != nil {
			metadata = map[string]any{}
		}
	}
	if _, ok := metadata["base_sha"]; !ok && baseSHA != "" {
		metadata["base_sha"] = baseSHA
	}
	if _, ok := metadata["start_sha"]; !ok && startSHA != "" {
		metadata["start_sha"] = startSHA
	}
	if _, ok := metadata["head_sha"]; !ok && headSHA != "" {
		metadata["head_sha"] = headSHA
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return location
	}
	location.PlatformMetadata = data
	return location
}

func locationSupportsInlineDiscussion(location CanonicalLocation) bool {
	path := strings.TrimSpace(location.Path)
	if path == "" {
		return false
	}
	if location.StartLine > 0 || location.EndLine > 0 {
		return true
	}
	return !strings.HasSuffix(path, "/")
}

func summaryBodyFromFinding(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	switch {
	case title != "" && body != "":
		return "### " + title + "\n\n" + body
	case body != "":
		return body
	default:
		return title
	}
}
