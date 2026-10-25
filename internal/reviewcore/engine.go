package reviewcore

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

type RunOptions struct {
	OutputMode    string
	PublishMode   string
	ReviewerPacks []string
	RouteOverride string
	AdvisorRoute  string
	// CoverageThreshold is the minimum reviewed-lines ratio for a passing
	// verdict; zero selects DefaultCoverageThreshold.
	CoverageThreshold float64
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
		Coverage:          coverageSummaryFromInput(input, opts.CoverageThreshold),
	}
	// INV-1019: a review that did not see enough of the change must not pass.
	// Findings that were found still produce requested_changes; only a clean
	// result is downgraded, because "nothing found" is not "nothing there".
	if bundle.Coverage != nil && bundle.Coverage.Incomplete {
		if v := strings.ToLower(strings.TrimSpace(bundle.Verdict)); v == "" || v == "pass" || v == "passed" || v == "approved" {
			bundle.Verdict = VerdictIncomplete
		}
		bundle.MarkdownSummary = appendCoverageNote(bundle.MarkdownSummary, bundle.Coverage)
	}
	if bundle.MarkdownSummary != "" {
		bundle.PublishCandidates = append(bundle.PublishCandidates, PublishCandidate{
			Kind: "summary",
			Body: bundle.MarkdownSummary,
		})
	}
	lineIndex := buildChangedLineIndex(input.Request)
	for _, finding := range decision.MergedFindings {
		bundle.PublishCandidates = append(bundle.PublishCandidates, publishCandidateForFinding(input, lineIndex, finding))
	}
	return bundle, nil
}

func publishCandidateForFinding(input ReviewInput, lineIndex changedLineIndex, finding Finding) PublishCandidate {
	location := locationWithGitLabVersionMetadata(input, finding.Identity.Location)
	if !locationSupportsInlineDiscussion(location) {
		return PublishCandidate{
			Kind:             "finding",
			Title:            finding.Title,
			Body:             summaryBodyFromFinding(finding.Title, finding.Body),
			Severity:         finding.Severity,
			PublishAsSummary: true,
			Location:         location,
		}
	}
	// INV-1018: an inline comment must land on a line this change touched.
	// A hallucinated or drifted line number is published at file level with
	// the original location kept for diagnostics, instead of failing at the
	// platform API.
	if lineIndex.verify(finding.Identity.Location) == locationOutOfDiff {
		return PublishCandidate{
			Kind:             "finding",
			Title:            finding.Title,
			Body:             outOfDiffBodyFromFinding(finding),
			Severity:         finding.Severity,
			PublishAsSummary: true,
			OutOfDiff:        true,
			Location:         location,
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

func outOfDiffBodyFromFinding(finding Finding) string {
	loc := finding.Identity.Location
	where := strings.TrimSpace(loc.Path)
	if loc.StartLine > 0 {
		where += ":" + strconv.Itoa(loc.StartLine)
		if loc.EndLine > loc.StartLine {
			where += "-" + strconv.Itoa(loc.EndLine)
		}
	}
	return summaryBodyFromFinding(finding.Title, finding.Body) + "\n\n_定位 " + where + " 不在本次改动范围内，已按文件级发布。_"
}

func coverageSummaryFromInput(input ReviewInput, threshold float64) *CoverageSummary {
	report := input.Coverage
	if report.TotalChangedLines == 0 && report.HunksTotal == 0 && len(report.ReviewedPaths) == 0 {
		return nil
	}
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultCoverageThreshold
	}
	ratio := report.Ratio
	if report.TotalChangedLines > 0 {
		ratio = float64(report.ReviewedChangedLines) / float64(report.TotalChangedLines)
	} else if ratio == 0 {
		ratio = 1
	}
	summary := &CoverageSummary{
		Mode:                 string(report.Mode),
		TotalChangedLines:    report.TotalChangedLines,
		ReviewedChangedLines: report.ReviewedChangedLines,
		HunksTotal:           report.HunksTotal,
		HunksInPrompt:        report.HunksInPrompt,
		Ratio:                ratio,
		Threshold:            threshold,
		Incomplete:           ratio < threshold,
		Summary:              report.Summary,
	}
	for _, skipped := range report.Skipped {
		summary.Skipped = append(summary.Skipped, SkippedScope{Path: skipped.Path, Reason: skipped.Reason, ChangedLines: skipped.ChangedLines})
	}
	return summary
}

// appendCoverageNote adds an "unreviewed scope" section to the summary so the
// platform comment says what the reviewer did not see.
func appendCoverageNote(summary string, coverage *CoverageSummary) string {
	var b strings.Builder
	if strings.TrimSpace(summary) != "" {
		b.WriteString(strings.TrimRight(summary, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("### 未评审范围\n\n")
	b.WriteString("本次评审只覆盖了 ")
	b.WriteString(strconv.Itoa(coverage.ReviewedChangedLines))
	b.WriteString("/")
	b.WriteString(strconv.Itoa(coverage.TotalChangedLines))
	b.WriteString(" 行改动（")
	b.WriteString(strconv.Itoa(int(coverage.Ratio*100 + 0.5)))
	b.WriteString("%），低于阈值 ")
	b.WriteString(strconv.Itoa(int(coverage.Threshold*100 + 0.5)))
	b.WriteString("%，结论为 incomplete，不代表未评审部分没有问题。\n")
	for _, skipped := range coverage.Skipped {
		b.WriteString("- `")
		b.WriteString(skipped.Path)
		b.WriteString("`：")
		b.WriteString(skipped.Reason)
		if skipped.ChangedLines > 0 {
			b.WriteString("，")
			b.WriteString(strconv.Itoa(skipped.ChangedLines))
			b.WriteString(" 行未评审")
		}
		b.WriteString("\n")
	}
	return b.String()
}
