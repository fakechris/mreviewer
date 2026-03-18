package context

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
)

const (
	defaultSchemaVersion      = "1.0"
	defaultContextLinesBefore = 20
	defaultContextLinesAfter  = 20
	defaultMaxChangedLines    = 2500
	defaultMaxFiles           = 80

	ExcludedReasonGenerated       = "generated_file"
	ExcludedReasonBinary          = "binary_file"
	ExcludedReasonVendor          = "vendor_path"
	ExcludedReasonLockFile        = "lock_file"
	ExcludedReasonTooLarge        = "too_large"
	ExcludedReasonPathNotIncluded = "path_not_included"
	ExcludedReasonPathExcluded    = "path_excluded"
	ExcludedReasonNoHunks         = "no_hunks"
	ExcludedReasonScopeLimit      = "scope_limit"
)

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

type Assembler struct{}

type AssembleInput struct {
	ReviewRunID       int64
	Project           ProjectContext
	MergeRequest      MergeRequestContext
	Version           VersionContext
	Rules             TrustedRules
	Settings          PolicySettings
	Diffs             []gitlab.MergeRequestDiff
	HistoricalContext HistoricalContext
}

type AssemblyResult struct {
	Request           ReviewRequest  `json:"request"`
	Excluded          []ExcludedFile `json:"excluded,omitempty"`
	Truncated         bool           `json:"truncated"`
	TotalChangedLines int            `json:"total_changed_lines"`
	Mode              ReviewMode     `json:"mode"`
	Coverage          CoverageReport `json:"coverage"`
}

type ReviewMode string

const (
	ReviewModeFullScope   ReviewMode = "full_scope"
	ReviewModeTruncated   ReviewMode = "truncated"
	ReviewModeDegradation ReviewMode = "degradation"
)

type ReviewRequest struct {
	SchemaVersion     string              `json:"schema_version"`
	ReviewRunID       string              `json:"review_run_id"`
	Project           ProjectContext      `json:"project"`
	MergeRequest      MergeRequestContext `json:"merge_request"`
	Version           VersionContext      `json:"version"`
	Rules             TrustedRules        `json:"rules"`
	Changes           []Change            `json:"changes,omitempty"`
	HistoricalContext HistoricalContext   `json:"historical_context,omitempty"`
}

type ProjectContext struct {
	ProjectID     int64  `json:"project_id"`
	FullPath      string `json:"full_path"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

type MergeRequestContext struct {
	IID         int64  `json:"iid"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
}

type VersionContext struct {
	BaseSHA    string `json:"base_sha,omitempty"`
	StartSHA   string `json:"start_sha,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	PatchIDSHA string `json:"patch_id_sha,omitempty"`
}

type TrustedRules struct {
	PlatformPolicy string `json:"platform_policy,omitempty"`
	ProjectPolicy  string `json:"project_policy,omitempty"`
	ReviewMarkdown string `json:"review_markdown,omitempty"`
	RulesDigest    string `json:"rules_digest,omitempty"`
}

type Change struct {
	Path         string `json:"path"`
	OldPath      string `json:"old_path,omitempty"`
	Status       string `json:"status"`
	Generated    bool   `json:"generated"`
	TooLarge     bool   `json:"too_large"`
	Collapsed    bool   `json:"collapsed"`
	Truncated    bool   `json:"truncated,omitempty"`
	ChangedLines int    `json:"changed_lines"`
	Hunks        []Hunk `json:"hunks,omitempty"`
}

type Hunk struct {
	OldStart      int      `json:"old_start"`
	OldLines      int      `json:"old_lines"`
	NewStart      int      `json:"new_start"`
	NewLines      int      `json:"new_lines"`
	Patch         string   `json:"patch"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
	ChangedLines  int      `json:"changed_lines"`
	Truncated     bool     `json:"truncated,omitempty"`
}

type HistoricalContext struct {
	ActiveBotFindings []HistoricalFinding `json:"active_bot_findings,omitempty"`
}

type HistoricalFinding struct {
	SemanticFingerprint string `json:"semantic_fingerprint,omitempty"`
	Title               string `json:"title,omitempty"`
	Path                string `json:"path,omitempty"`
	BodyMarkdown        string `json:"body_markdown,omitempty"`
	DiscussionID        string `json:"discussion_id,omitempty"`
	DiscussionType      string `json:"discussion_type,omitempty"`
	Resolved            bool   `json:"resolved,omitempty"`
}

type ExcludedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type CoverageReport struct {
	ReviewedPaths []string `json:"reviewed_paths,omitempty"`
	SkippedFiles  int      `json:"skipped_files,omitempty"`
	SkippedLines  int      `json:"skipped_lines,omitempty"`
	Summary       string   `json:"summary,omitempty"`
}

type PolicySettings struct {
	IncludePaths       []string `json:"include_paths,omitempty"`
	ExcludePaths       []string `json:"exclude_paths,omitempty"`
	ContextLinesBefore int      `json:"context_lines_before"`
	ContextLinesAfter  int      `json:"context_lines_after"`
	MaxChangedLines    int      `json:"max_changed_lines"`
	MaxFiles           int      `json:"max_files"`
}

type HistoricalStore interface {
	ListActiveFindingsByMR(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error)
	GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error)
}

func NewAssembler() *Assembler {
	return &Assembler{}
}

func DefaultPolicySettings() PolicySettings {
	return PolicySettings{
		ContextLinesBefore: defaultContextLinesBefore,
		ContextLinesAfter:  defaultContextLinesAfter,
		MaxChangedLines:    defaultMaxChangedLines,
		MaxFiles:           defaultMaxFiles,
	}
}

func SettingsFromPolicy(policy *db.ProjectPolicy) (PolicySettings, error) {
	settings := DefaultPolicySettings()
	if policy == nil {
		return settings, nil
	}

	include, err := decodePathList(policy.IncludePaths)
	if err != nil {
		return PolicySettings{}, fmt.Errorf("context: decode include_paths: %w", err)
	}
	exclude, err := decodePathList(policy.ExcludePaths)
	if err != nil {
		return PolicySettings{}, fmt.Errorf("context: decode exclude_paths: %w", err)
	}
	settings.IncludePaths = include
	settings.ExcludePaths = exclude

	var extra struct {
		MaxFiles        int `json:"max_files"`
		MaxChangedLines int `json:"max_changed_lines"`
		Context         struct {
			LinesBefore int `json:"lines_before"`
			LinesAfter  int `json:"lines_after"`
		} `json:"context"`
		Review struct {
			MaxFiles        int `json:"max_files"`
			MaxChangedLines int `json:"max_changed_lines"`
			Context         struct {
				LinesBefore int `json:"lines_before"`
				LinesAfter  int `json:"lines_after"`
			} `json:"context"`
		} `json:"review"`
	}
	if len(policy.Extra) > 0 && string(policy.Extra) != "null" {
		if err := json.Unmarshal(policy.Extra, &extra); err != nil {
			return PolicySettings{}, fmt.Errorf("context: decode policy extra: %w", err)
		}
	}

	applyPolicyExtra(&settings, extra.MaxFiles, extra.MaxChangedLines, extra.Context.LinesBefore, extra.Context.LinesAfter)
	applyPolicyExtra(&settings, extra.Review.MaxFiles, extra.Review.MaxChangedLines, extra.Review.Context.LinesBefore, extra.Review.Context.LinesAfter)

	return settings.withDefaults(), nil
}

func LoadHistoricalContext(ctx context.Context, store HistoricalStore, mergeRequestID int64) (HistoricalContext, error) {
	if store == nil {
		return HistoricalContext{}, nil
	}

	findings, err := store.ListActiveFindingsByMR(ctx, mergeRequestID)
	if err != nil {
		return HistoricalContext{}, fmt.Errorf("context: list active findings: %w", err)
	}

	historical := HistoricalContext{ActiveBotFindings: make([]HistoricalFinding, 0, len(findings))}
	for _, finding := range findings {
		entry := HistoricalFinding{
			SemanticFingerprint: finding.SemanticFingerprint,
			Title:               finding.Title,
			Path:                finding.Path,
			DiscussionID:        finding.GitlabDiscussionID,
		}
		if finding.BodyMarkdown.Valid {
			entry.BodyMarkdown = finding.BodyMarkdown.String
		}

		discussion, err := store.GetGitlabDiscussionByMergeRequestAndFinding(ctx, db.GetGitlabDiscussionByMergeRequestAndFindingParams{
			MergeRequestID:  mergeRequestID,
			ReviewFindingID: finding.ID,
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return HistoricalContext{}, fmt.Errorf("context: load discussion for finding %d: %w", finding.ID, err)
		}
		if err == nil {
			entry.DiscussionID = discussion.GitlabDiscussionID
			entry.DiscussionType = discussion.DiscussionType
			entry.Resolved = discussion.Resolved
		}

		historical.ActiveBotFindings = append(historical.ActiveBotFindings, entry)
	}

	return historical, nil
}

func (a *Assembler) Assemble(input AssembleInput) (AssemblyResult, error) {
	settings, err := prepareSettings(input.Settings)
	if err != nil {
		return AssemblyResult{}, err
	}

	result := AssemblyResult{
		Request: ReviewRequest{
			SchemaVersion:     defaultSchemaVersion,
			ReviewRunID:       strconv.FormatInt(input.ReviewRunID, 10),
			Project:           input.Project,
			MergeRequest:      input.MergeRequest,
			Version:           input.Version,
			Rules:             input.Rules,
			HistoricalContext: input.HistoricalContext,
		},
	}

	for _, diff := range input.Diffs {
		path := changePath(diff)
		if path == "" {
			continue
		}
		result.Coverage.SkippedLines += countChangedLines(diff.Diff)

		reason := excludedReason(path, diff, settings)
		if reason != "" {
			result.Excluded = append(result.Excluded, ExcludedFile{Path: path, Reason: reason})
			result.Coverage.SkippedFiles++
			continue
		}

		if len(result.Request.Changes) >= settings.maxFiles {
			result.Truncated = true
			result.Mode = ReviewModeDegradation
			result.Excluded = append(result.Excluded, ExcludedFile{Path: path, Reason: ExcludedReasonScopeLimit})
			result.Coverage.SkippedFiles++
			continue
		}

		parsedHunks, err := parseHunks(diff.Diff)
		if err != nil {
			return AssemblyResult{}, fmt.Errorf("context: parse hunks for %s: %w", path, err)
		}
		if len(parsedHunks) == 0 {
			result.Excluded = append(result.Excluded, ExcludedFile{Path: path, Reason: ExcludedReasonNoHunks})
			result.Coverage.SkippedFiles++
			continue
		}

		change := Change{
			Path:      path,
			OldPath:   normalizePath(diff.OldPath),
			Status:    changeStatus(diff),
			Generated: diff.GeneratedFile,
			TooLarge:  diff.TooLarge,
			Collapsed: diff.Collapsed,
		}

		remaining := settings.maxChangedLines - result.TotalChangedLines
		for _, parsed := range parsedHunks {
			if remaining <= 0 {
				result.Truncated = true
				change.Truncated = true
				break
			}

			truncatedHunk := false
			if parsed.changedLines() > remaining {
				parsed = parsed.truncate(remaining, settings.contextLinesAfter)
				truncatedHunk = true
			}

			hunk := parsed.toHunk(settings)
			if hunk.ChangedLines == 0 {
				continue
			}
			if truncatedHunk {
				hunk.Truncated = true
				change.Truncated = true
				result.Truncated = true
			}

			change.Hunks = append(change.Hunks, hunk)
			change.ChangedLines += hunk.ChangedLines
			result.TotalChangedLines += hunk.ChangedLines
			remaining = settings.maxChangedLines - result.TotalChangedLines
		}

		if len(change.Hunks) == 0 {
			if change.Truncated {
				result.Excluded = append(result.Excluded, ExcludedFile{Path: path, Reason: ExcludedReasonScopeLimit})
				result.Coverage.SkippedFiles++
			}
			continue
		}

		result.Request.Changes = append(result.Request.Changes, change)
		result.Coverage.ReviewedPaths = append(result.Coverage.ReviewedPaths, change.Path)
		result.Coverage.SkippedLines -= change.ChangedLines
	}

	if result.Mode == "" {
		switch {
		case len(result.Excluded) > 0 && hasScopeLimitExclusion(result.Excluded):
			result.Mode = ReviewModeDegradation
		case result.Truncated:
			result.Mode = ReviewModeTruncated
		default:
			result.Mode = ReviewModeFullScope
		}
	}
	result.Coverage.Summary = summarizeCoverage(result)
	return result, nil
}

type compiledSettings struct {
	PolicySettings
	include           []*regexp.Regexp
	exclude           []*regexp.Regexp
	contextLinesAfter int
	maxChangedLines   int
	maxFiles          int
}

func prepareSettings(settings PolicySettings) (compiledSettings, error) {
	settings = settings.withDefaults()
	compiled := compiledSettings{
		PolicySettings:    settings,
		contextLinesAfter: settings.ContextLinesAfter,
		maxChangedLines:   settings.MaxChangedLines,
		maxFiles:          settings.MaxFiles,
	}

	var err error
	compiled.include, err = compilePatterns(settings.IncludePaths)
	if err != nil {
		return compiledSettings{}, fmt.Errorf("context: compile include paths: %w", err)
	}
	compiled.exclude, err = compilePatterns(settings.ExcludePaths)
	if err != nil {
		return compiledSettings{}, fmt.Errorf("context: compile exclude paths: %w", err)
	}

	return compiled, nil
}

func (s PolicySettings) withDefaults() PolicySettings {
	if s.ContextLinesBefore <= 0 {
		s.ContextLinesBefore = defaultContextLinesBefore
	}
	if s.ContextLinesAfter <= 0 {
		s.ContextLinesAfter = defaultContextLinesAfter
	}
	if s.MaxChangedLines <= 0 {
		s.MaxChangedLines = defaultMaxChangedLines
	}
	if s.MaxFiles <= 0 {
		s.MaxFiles = defaultMaxFiles
	}
	return s
}

func applyPolicyExtra(settings *PolicySettings, maxFiles, maxChangedLines, linesBefore, linesAfter int) {
	if maxFiles > 0 {
		settings.MaxFiles = maxFiles
	}
	if maxChangedLines > 0 {
		settings.MaxChangedLines = maxChangedLines
	}
	if linesBefore > 0 {
		settings.ContextLinesBefore = linesBefore
	}
	if linesAfter > 0 {
		settings.ContextLinesAfter = linesAfter
	}
}

func decodePathList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = normalizePath(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(globToRegexp(pattern))
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func globToRegexp(pattern string) string {
	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i++
			} else {
				builder.WriteString(`[^/]*`)
			}
		case '?':
			builder.WriteString(`[^/]`)
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			builder.WriteByte('\\')
			builder.WriteByte(ch)
		default:
			builder.WriteByte(ch)
		}
	}
	builder.WriteString("$")
	return builder.String()
}

func excludedReason(path string, diff gitlab.MergeRequestDiff, settings compiledSettings) string {
	if diff.GeneratedFile {
		return ExcludedReasonGenerated
	}
	if diff.TooLarge {
		return ExcludedReasonTooLarge
	}
	if isBinaryFile(path, diff.Diff) {
		return ExcludedReasonBinary
	}
	if isVendorPath(path) {
		return ExcludedReasonVendor
	}
	if isLockFile(path) {
		return ExcludedReasonLockFile
	}
	if len(settings.include) > 0 && !matchesAny(path, settings.include) {
		return ExcludedReasonPathNotIncluded
	}
	if len(settings.exclude) > 0 && matchesAny(path, settings.exclude) {
		return ExcludedReasonPathExcluded
	}
	return ""
}

func matchesAny(path string, patterns []*regexp.Regexp) bool {
	path = normalizePath(path)
	for _, pattern := range patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

func changePath(diff gitlab.MergeRequestDiff) string {
	if path := normalizePath(diff.NewPath); path != "" {
		return path
	}
	return normalizePath(diff.OldPath)
}

func changeStatus(diff gitlab.MergeRequestDiff) string {
	switch {
	case diff.NewFile:
		return "added"
	case diff.DeletedFile:
		return "deleted"
	case diff.RenamedFile:
		return "renamed"
	default:
		return "modified"
	}
}

func normalizePath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")
	return value
}

func isVendorPath(path string) bool {
	path = normalizePath(path)
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "vendor" {
			return true
		}
	}
	return false
}

func isLockFile(path string) bool {
	path = normalizePath(path)
	base := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
	if strings.HasSuffix(base, ".lock") {
		return true
	}
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "gemfile.lock", "composer.lock", "pipfile.lock", "poetry.lock", "podfile.lock":
		return true
	default:
		return false
	}
}

func isBinaryFile(path, diffText string) bool {
	lowerPath := strings.ToLower(normalizePath(path))
	for _, suffix := range []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp", ".pdf", ".zip", ".gz", ".tar", ".tgz", ".7z", ".jar", ".class", ".so", ".dll", ".dylib", ".exe", ".bin", ".mp3", ".mp4", ".mov", ".avi", ".woff", ".woff2", ".ttf", ".otf", ".eot"} {
		if strings.HasSuffix(lowerPath, suffix) {
			return true
		}
	}
	lowerDiff := strings.ToLower(diffText)
	return strings.Contains(lowerDiff, "binary files") || strings.Contains(lowerDiff, "git binary patch") || strings.Contains(diffText, "\x00")
}

type parsedHunk struct {
	oldStart int
	newStart int
	trailer  string
	lines    []string
}

func parseHunks(diffText string) ([]parsedHunk, error) {
	diffText = strings.ReplaceAll(diffText, "\r\n", "\n")
	diffText = strings.TrimRight(diffText, "\n")
	if strings.TrimSpace(diffText) == "" {
		return nil, nil
	}

	var hunks []parsedHunk
	var current *parsedHunk
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "@@") {
			matches := hunkHeaderPattern.FindStringSubmatch(line)
			if matches == nil {
				return nil, fmt.Errorf("invalid hunk header %q", line)
			}
			oldStart, _ := strconv.Atoi(matches[1])
			newStart, _ := strconv.Atoi(matches[3])
			hunks = append(hunks, parsedHunk{oldStart: oldStart, newStart: newStart, trailer: matches[5]})
			current = &hunks[len(hunks)-1]
			continue
		}
		if current != nil {
			current.lines = append(current.lines, line)
		}
	}
	return hunks, nil
}

func (h parsedHunk) changedLines() int {
	count := 0
	for _, line := range h.lines {
		if isChangeLine(line) {
			count++
		}
	}
	return count
}

func (h parsedHunk) truncate(remaining, trailingContext int) parsedHunk {
	if remaining <= 0 || h.changedLines() <= remaining {
		return h
	}

	truncated := parsedHunk{oldStart: h.oldStart, newStart: h.newStart, trailer: h.trailer}
	changesSeen := 0
	for idx := 0; idx < len(h.lines); idx++ {
		line := h.lines[idx]
		if isChangeLine(line) {
			if changesSeen >= remaining {
				break
			}
			changesSeen++
			truncated.lines = append(truncated.lines, line)
			if changesSeen == remaining {
				addedContext := 0
				for next := idx + 1; next < len(h.lines); next++ {
					nextLine := h.lines[next]
					if strings.HasPrefix(nextLine, " ") {
						if addedContext >= trailingContext {
							break
						}
						truncated.lines = append(truncated.lines, nextLine)
						addedContext++
						continue
					}
					if nextLine == `\ No newline at end of file` {
						truncated.lines = append(truncated.lines, nextLine)
						continue
					}
					break
				}
				break
			}
			continue
		}
		truncated.lines = append(truncated.lines, line)
	}

	return truncated
}

func (h parsedHunk) toHunk(settings compiledSettings) Hunk {
	oldLines, newLines := countLineSpans(h.lines)
	before, after := surroundingContext(h.lines, settings.ContextLinesBefore, settings.ContextLinesAfter)
	patchLines := append([]string{formatHunkHeader(h.oldStart, oldLines, h.newStart, newLines, h.trailer)}, h.lines...)
	return Hunk{
		OldStart:      h.oldStart,
		OldLines:      oldLines,
		NewStart:      h.newStart,
		NewLines:      newLines,
		Patch:         strings.Join(patchLines, "\n"),
		ContextBefore: before,
		ContextAfter:  after,
		ChangedLines:  h.changedLines(),
	}
}

func countLineSpans(lines []string) (oldLines, newLines int) {
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch line[0] {
		case ' ':
			oldLines++
			newLines++
		case '-':
			oldLines++
		case '+':
			newLines++
		}
	}
	return oldLines, newLines
}

func surroundingContext(lines []string, beforeLimit, afterLimit int) ([]string, []string) {
	firstChange := -1
	lastChange := -1
	for i, line := range lines {
		if isChangeLine(line) {
			if firstChange == -1 {
				firstChange = i
			}
			lastChange = i
		}
	}
	if firstChange == -1 {
		return nil, nil
	}

	var before []string
	for i := 0; i < firstChange; i++ {
		if strings.HasPrefix(lines[i], " ") {
			before = append(before, strings.TrimPrefix(lines[i], " "))
		}
	}
	if beforeLimit > 0 && len(before) > beforeLimit {
		before = before[len(before)-beforeLimit:]
	}

	var after []string
	for i := lastChange + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], " ") {
			after = append(after, strings.TrimPrefix(lines[i], " "))
		}
	}
	if afterLimit > 0 && len(after) > afterLimit {
		after = after[:afterLimit]
	}

	return before, after
}

func formatHunkHeader(oldStart, oldLines, newStart, newLines int, trailer string) string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@%s", oldStart, oldLines, newStart, newLines, trailer)
}

func isChangeLine(line string) bool {
	return strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")
}

func countChangedLines(diffText string) int {
	hunks, err := parseHunks(diffText)
	if err != nil {
		return 0
	}
	total := 0
	for _, hunk := range hunks {
		total += hunk.changedLines()
	}
	return total
}

func hasScopeLimitExclusion(excluded []ExcludedFile) bool {
	for _, file := range excluded {
		if file.Reason == ExcludedReasonScopeLimit {
			return true
		}
	}
	return false
}

func summarizeCoverage(result AssemblyResult) string {
	if result.Mode == ReviewModeDegradation {
		return fmt.Sprintf("Partial coverage: reviewed %d file(s), skipped %d file(s) and %d changed line(s) because the merge request exceeded review limits.", len(result.Coverage.ReviewedPaths), result.Coverage.SkippedFiles, result.Coverage.SkippedLines)
	}
	if result.Mode == ReviewModeTruncated {
		return fmt.Sprintf("Full-scope review with truncation: reviewed %d file(s) and truncated context after %d changed line(s).", len(result.Coverage.ReviewedPaths), result.TotalChangedLines)
	}
	return fmt.Sprintf("Full coverage: reviewed %d file(s).", len(result.Coverage.ReviewedPaths))
}
