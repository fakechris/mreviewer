package compare

import (
	"fmt"
	"sort"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/textutil"
)

type ExternalComment struct {
	ExternalID string
	Category   string
	Severity   string
	Title      string
	Body       string
	Claim      string
	Confidence float64
	Path       string
	Side       core.DiffSide
	StartLine  int
	EndLine    int
	Standards  []string
}

type ExternalArtifactInput struct {
	ReviewerID   string
	ReviewerKind string
	Target       core.ReviewTarget
	Summary      string
	Comments     []ExternalComment
}

type SharedFinding struct {
	IdentityKey string            `json:"identity_key"`
	Finding     core.Finding      `json:"finding"`
	Reviewers   []string          `json:"reviewers"`
	Target      core.ReviewTarget `json:"target"`
}

type Report struct {
	Target             core.ReviewTarget         `json:"target"`
	ReviewerCount      int                       `json:"reviewer_count"`
	UniqueFindingCount int                       `json:"unique_finding_count"`
	AgreementRate      float64                   `json:"agreement_rate"`
	SharedFindings     []SharedFinding           `json:"shared_findings,omitempty"`
	UniqueByReviewer   map[string][]core.Finding `json:"unique_by_reviewer,omitempty"`
}

type AggregateReport struct {
	TargetCount          int      `json:"target_count"`
	TotalReviewerCount   int      `json:"total_reviewer_count"`
	TotalUniqueFindings  int      `json:"total_unique_findings"`
	AverageAgreementRate float64  `json:"average_agreement_rate"`
	Reports              []Report `json:"reports,omitempty"`
}

func RenderMarkdown(report Report) string {
	lines := []string{
		"## Comparison",
		"",
		fmt.Sprintf("- Reviewers: %d", report.ReviewerCount),
		fmt.Sprintf("- Unique findings: %d", report.UniqueFindingCount),
		fmt.Sprintf("- Agreement rate: %.2f", report.AgreementRate),
	}
	return strings.Join(lines, "\n")
}

func RenderAggregateMarkdown(report AggregateReport) string {
	if report.TargetCount == 0 {
		return ""
	}
	lines := []string{
		"## Aggregate Comparison",
		"",
		fmt.Sprintf("- Targets: %d", report.TargetCount),
		fmt.Sprintf("- Reviewers: %d", report.TotalReviewerCount),
		fmt.Sprintf("- Unique findings: %d", report.TotalUniqueFindings),
		fmt.Sprintf("- Average agreement rate: %.2f", report.AverageAgreementRate),
	}
	return strings.Join(lines, "\n")
}

func NormalizeExternalArtifact(input ExternalArtifactInput) core.ReviewerArtifact {
	artifact := core.ReviewerArtifact{
		ReviewerID:   strings.TrimSpace(input.ReviewerID),
		ReviewerKind: strings.TrimSpace(input.ReviewerKind),
		Target:       input.Target,
		Summary:      strings.TrimSpace(input.Summary),
	}
	for _, comment := range input.Comments {
		artifact.Findings = append(artifact.Findings, normalizeExternalFinding(comment))
	}
	return artifact
}

func CompareArtifacts(artifacts []core.ReviewerArtifact) Report {
	report := Report{
		UniqueByReviewer: make(map[string][]core.Finding),
	}
	if len(artifacts) == 0 {
		return report
	}
	report.Target = artifacts[0].Target
	report.ReviewerCount = len(artifacts)

	type seenFinding struct {
		finding   core.Finding
		reviewers map[string]struct{}
	}
	seen := make(map[string]*seenFinding)

	for idx, artifact := range artifacts {
		reviewer := strings.TrimSpace(artifact.ReviewerID)
		if reviewer == "" {
			reviewer = fmt.Sprintf("unknown-%d", idx+1)
		}
		localSeen := make(map[string]struct{})
		for _, finding := range artifact.Findings {
			if !hasStableIdentity(finding.Identity) {
				continue
			}
			key := identityKey(finding.Identity)
			entry, ok := seen[key]
			if !ok {
				entry = &seenFinding{finding: finding, reviewers: make(map[string]struct{})}
				seen[key] = entry
			}
			entry.reviewers[reviewer] = struct{}{}
			localSeen[key] = struct{}{}
		}
		for key, entry := range seen {
			if _, ok := entry.reviewers[reviewer]; ok {
				if _, ok := localSeen[key]; !ok {
					continue
				}
			}
		}
	}

	report.UniqueFindingCount = len(seen)
	if report.UniqueFindingCount == 0 {
		return report
	}

	sharedCount := 0
	for key, entry := range seen {
		reviewers := reviewerList(entry.reviewers)
		if len(reviewers) > 1 {
			sharedCount++
			report.SharedFindings = append(report.SharedFindings, SharedFinding{
				IdentityKey: key,
				Finding:     entry.finding,
				Reviewers:   reviewers,
				Target:      report.Target,
			})
			continue
		}
		report.UniqueByReviewer[reviewers[0]] = append(report.UniqueByReviewer[reviewers[0]], entry.finding)
	}
	sort.Slice(report.SharedFindings, func(i, j int) bool {
		return report.SharedFindings[i].IdentityKey < report.SharedFindings[j].IdentityKey
	})
	report.AgreementRate = float64(sharedCount) / float64(report.UniqueFindingCount)
	return report
}

func hasStableIdentity(identity core.FindingIdentityInput) bool {
	if strings.TrimSpace(identity.NormalizedClaim) != "" || strings.TrimSpace(identity.Location.Path) != "" {
		return true
	}
	return identity.Location.StartLine > 0 || identity.Location.EndLine > 0
}

func AggregateReports(reports []Report) AggregateReport {
	aggregate := AggregateReport{
		Reports: append([]Report(nil), reports...),
	}
	if len(reports) == 0 {
		return aggregate
	}
	aggregate.TargetCount = len(reports)
	totalAgreement := 0.0
	for _, report := range reports {
		aggregate.TotalReviewerCount += report.ReviewerCount
		aggregate.TotalUniqueFindings += report.UniqueFindingCount
		totalAgreement += report.AgreementRate
	}
	aggregate.AverageAgreementRate = totalAgreement / float64(len(reports))
	return aggregate
}

func normalizeExternalFinding(comment ExternalComment) core.Finding {
	location := core.CanonicalLocation{
		Path:      strings.TrimSpace(comment.Path),
		Side:      comment.Side,
		StartLine: comment.StartLine,
		EndLine:   comment.EndLine,
	}
	claim := normalizedClaim(comment)
	return core.Finding{
		Category:   normalizedToken(comment.Category),
		Severity:   normalizedToken(comment.Severity),
		Title:      strings.TrimSpace(comment.Title),
		Body:       strings.TrimSpace(comment.Body),
		Claim:      textutil.FirstNonEmpty(strings.TrimSpace(comment.Claim), strings.TrimSpace(comment.Body), strings.TrimSpace(comment.Title)),
		Confidence: comment.Confidence,
		Identity: core.FindingIdentityInput{
			Category:            normalizedToken(comment.Category),
			NormalizedClaim:     claim,
			EvidenceFingerprint: fmt.Sprintf("%s:%s:%d:%d", strings.TrimSpace(comment.Path), comment.Side, comment.StartLine, comment.EndLine),
			Standards:           normalizeStandards(comment.Standards),
			Location:            location,
		},
	}
}

func identityKey(identity core.FindingIdentityInput) string {
	return strings.Join([]string{
		normalizedToken(identity.Category),
		strings.TrimSpace(identity.NormalizedClaim),
		strings.TrimSpace(identity.Location.Path),
		string(identity.Location.Side),
		fmt.Sprintf("%d", identity.Location.StartLine),
		fmt.Sprintf("%d", identity.Location.EndLine),
	}, "|")
}

func normalizedClaim(comment ExternalComment) string {
	return normalizedText(textutil.FirstNonEmpty(comment.Claim, comment.Body, comment.Title))
}

func normalizedText(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	replacer := strings.NewReplacer("\n", " ", "\t", " ", "\r", " ", "`", "", "*", "", "_", "")
	raw = replacer.Replace(raw)
	return strings.Join(strings.Fields(raw), " ")
}

func normalizedToken(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeStandards(standards []string) []string {
	if len(standards) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, standard := range standards {
		token := normalizedToken(standard)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func reviewerList(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for reviewer := range m {
		out = append(out, reviewer)
	}
	sort.Strings(out)
	return out
}
