package reviewrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/llm"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type ReviewInputLoader interface {
	Load(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error)
}

type ReviewEngine interface {
	Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error)
}

type RunWriteback interface {
	Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error
}

type BundleWriteback interface {
	WriteBundle(ctx context.Context, run db.ReviewRun, bundle core.ReviewBundle) error
}

type EngineProcessor struct {
	db                   *sql.DB
	loadInput            ReviewInputLoader
	engine               ReviewEngine
	newStore             func(db.DBTX) db.Store
	writeback            RunWriteback
	defaultReviewerPacks []string
	defaultAdvisorRoute  string
}

func NewEngineProcessor(sqlDB *sql.DB, loadInput ReviewInputLoader, engine ReviewEngine) *EngineProcessor {
	return &EngineProcessor{
		db:        sqlDB,
		loadInput: loadInput,
		engine:    engine,
		newStore:  defaultReviewRunNewStore,
	}
}

func NewEngineProcessorWithStoreFactory(sqlDB *sql.DB, loadInput ReviewInputLoader, engine ReviewEngine, newStore func(db.DBTX) db.Store) *EngineProcessor {
	if newStore == nil {
		newStore = defaultReviewRunNewStore
	}
	return &EngineProcessor{
		db:        sqlDB,
		loadInput: loadInput,
		engine:    engine,
		newStore:  newStore,
	}
}

func (p *EngineProcessor) WithWriteback(writeback RunWriteback) *EngineProcessor {
	p.writeback = writeback
	return p
}

func (p *EngineProcessor) WithDefaultReviewerPacks(packIDs []string) *EngineProcessor {
	p.defaultReviewerPacks = append([]string(nil), packIDs...)
	return p
}

func (p *EngineProcessor) WithDefaultAdvisorRoute(route string) *EngineProcessor {
	p.defaultAdvisorRoute = strings.TrimSpace(route)
	return p
}

func (p *EngineProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	if p == nil || p.db == nil || p.loadInput == nil || p.engine == nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: engine processor dependencies are not configured")
	}

	store := p.newStore(p.db)
	mr, err := store.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: load merge request: %w", err)
	}
	project, err := store.GetProject(ctx, run.ProjectID)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: load project: %w", err)
	}
	instance, err := store.GetGitlabInstance(ctx, project.GitlabInstanceID)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: load gitlab instance: %w", err)
	}

	scope := runScopeFromJSON(run.ScopeJson)
	platform := normalizePlatform(scope.Platform)
	if platform == "" {
		platform = inferPlatformFromWebURL(mr.WebUrl)
	}
	if platform == "" {
		platform = core.PlatformGitLab
	}
	target := core.ReviewTarget{
		Platform:     platform,
		URL:          mr.WebUrl,
		Repository:   project.PathWithNamespace,
		ProjectID:    project.GitlabProjectID,
		ChangeNumber: mr.MrIid,
		BaseURL:      instance.Url,
	}
	providerRoute := providerRouteFromScope(run.ScopeJson)
	input, err := p.loadInput.Load(ctx, target, providerRoute)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: load review input: %w", err)
	}
	if err := persistIdentityMappingsFromInput(ctx, store, run, input); err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: persist identity mappings: %w", err)
	}

	bundle, err := p.engine.Run(ctx, input, core.RunOptions{
		OutputMode:    "both",
		PublishMode:   "full-review-comments",
		ReviewerPacks: append([]string(nil), p.defaultReviewerPacks...),
		RouteOverride: providerRoute,
		AdvisorRoute:  p.defaultAdvisorRoute,
	})
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: run review engine: %w", err)
	}
	if err := persistMRVersionFromInput(ctx, store, mr.ID, input.Snapshot.Version, input.Request.Version); err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: persist mr version: %w", err)
	}

	result := legacyResultFromBundle(run, bundle)
	reviewedPaths, deletedPaths := reviewedScopeFromRequest(input.Request)
	findings, finalStatus, err := llm.PersistReviewResult(ctx, llm.NewSQLProcessorStore(p.db), run, mr, result, reviewedPaths, deletedPaths, nil)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: persist review result: %w", err)
	}
	if p.writeback != nil {
		updatedRun, loadErr := store.GetReviewRun(ctx, run.ID)
		if loadErr != nil {
			return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: load updated review run: %w", loadErr)
		}
		if bundleWriteback, ok := p.writeback.(BundleWriteback); ok {
			if err := bundleWriteback.WriteBundle(ctx, updatedRun, bundle); err != nil {
				return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: write review bundle comments: %w", err)
			}
		} else if err := p.writeback.Write(ctx, updatedRun, findings); err != nil {
			return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: write review comments: %w", err)
		}
	}

	return scheduler.ProcessOutcome{
		Status:         finalStatus,
		ReviewFindings: findings,
		ReviewBundle:   bundle,
	}, nil
}

func persistIdentityMappingsFromInput(ctx context.Context, store db.Store, run db.ReviewRun, input core.ReviewInput) error {
	if store == nil {
		return nil
	}
	projectPath := strings.TrimSpace(input.Target.Repository)
	if projectPath == "" {
		projectPath = strings.TrimSpace(input.Snapshot.Target.Repository)
	}
	platform := string(normalizePlatform(input.Target.Platform))
	if platform == "" {
		platform = string(normalizePlatform(input.Snapshot.Target.Platform))
	}
	if projectPath == "" || platform == "" {
		return nil
	}
	platformUsername := strings.TrimSpace(input.Snapshot.Change.Author.Username)
	platformUserID := strings.TrimSpace(input.Snapshot.Change.Author.UserID)
	headSHA := firstNonEmpty(
		strings.TrimSpace(input.Snapshot.HeadCommit.SHA),
		strings.TrimSpace(input.Snapshot.Change.HeadSHA),
		strings.TrimSpace(input.Snapshot.Version.HeadSHA),
		strings.TrimSpace(run.HeadSha),
	)
	observations := []struct {
		role   string
		author core.PlatformAuthor
	}{
		{role: "commit_author", author: input.Snapshot.HeadCommit.Author},
		{role: "commit_committer", author: input.Snapshot.HeadCommit.Committer},
	}
	for _, observation := range observations {
		identityKey := gitIdentityKey(observation.author)
		if identityKey == "" {
			continue
		}
		observationUsername := strings.TrimSpace(observation.author.Username)
		observationUserID := strings.TrimSpace(observation.author.UserID)
		if observationUsername == "" {
			observationUsername = platformUsername
		}
		if observationUserID == "" {
			observationUserID = platformUserID
		}
		status := "auto"
		confidence := 0.72
		if observationUsername == "" {
			status = "unresolved"
			confidence = 0.55
		} else if strings.TrimSpace(observation.author.Email) != "" {
			confidence = 0.94
		}
		if err := store.UpsertIdentityMapping(ctx, db.UpsertIdentityMappingParams{
			Platform:         platform,
			ProjectPath:      projectPath,
			GitIdentityKey:   identityKey,
			GitEmail:         strings.TrimSpace(observation.author.Email),
			GitName:          strings.TrimSpace(observation.author.Name),
			ObservedRole:     observation.role,
			PlatformUsername: observationUsername,
			PlatformUserID:   observationUserID,
			HeadSha:          headSHA,
			Confidence:       confidence,
			Source:           "observed",
			Status:           status,
			LastSeenRunID:    sql.NullInt64{Int64: run.ID, Valid: run.ID > 0},
		}); err != nil {
			return err
		}
	}
	return nil
}

func gitIdentityKey(author core.PlatformAuthor) string {
	if email := strings.ToLower(strings.TrimSpace(author.Email)); email != "" {
		return "email:" + email
	}
	if username := strings.ToLower(strings.TrimSpace(author.Username)); username != "" {
		return "username:" + username
	}
	if name := strings.ToLower(strings.TrimSpace(author.Name)); name != "" {
		return "name:" + name
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func legacyResultFromBundle(run db.ReviewRun, bundle core.ReviewBundle) llm.ReviewResult {
	findings := make([]llm.ReviewFinding, 0, len(bundle.PublishCandidates))
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Kind != "finding" {
			continue
		}
		finding := llm.ReviewFinding{
			Category:     "review",
			Severity:     strings.TrimSpace(candidate.Severity),
			Title:        strings.TrimSpace(candidate.Title),
			BodyMarkdown: strings.TrimSpace(candidate.Body),
			Path:         strings.TrimSpace(candidate.Location.Path),
			NewLine:      int32Ptr(candidate.Location.StartLine),
		}
		if finding.Severity == "" {
			finding.Severity = "medium"
		}
		if finding.Path == "" {
			finding.AnchorKind = "general"
		} else {
			finding.AnchorKind = "new_line"
		}
		findings = append(findings, finding)
	}

	return llm.ReviewResult{
		SchemaVersion: bundle.JSONSchemaVersion,
		ReviewRunID:   fmt.Sprintf("%d", run.ID),
		Summary:       strings.TrimSpace(bundle.MarkdownSummary),
		Findings:      findings,
		Status:        bundle.Verdict,
	}
}

func reviewedScopeFromRequest(request ctxpkg.ReviewRequest) (map[string]struct{}, map[string]struct{}) {
	reviewedPaths := make(map[string]struct{}, len(request.Changes))
	deletedPaths := make(map[string]struct{})
	for _, change := range request.Changes {
		path := normalizePath(change.Path)
		if path == "" {
			continue
		}
		reviewedPaths[path] = struct{}{}
		if strings.EqualFold(strings.TrimSpace(change.Status), "deleted") {
			deletedPaths[path] = struct{}{}
		}
	}
	return reviewedPaths, deletedPaths
}

type runScope struct {
	ProviderRoute string        `json:"provider_route"`
	Platform      core.Platform `json:"platform"`
}

func providerRouteFromScope(raw []byte) string {
	return strings.TrimSpace(runScopeFromJSON(raw).ProviderRoute)
}

func runScopeFromJSON(raw []byte) runScope {
	var scope runScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		return runScope{}
	}
	return scope
}

func inferPlatformFromWebURL(webURL string) core.Platform {
	lower := strings.ToLower(strings.TrimSpace(webURL))
	switch {
	case strings.Contains(lower, "/pull/"):
		return core.PlatformGitHub
	case strings.Contains(lower, "/-/merge_requests/"):
		return core.PlatformGitLab
	default:
		return ""
	}
}

func normalizePlatform(platform core.Platform) core.Platform {
	value := strings.ToLower(strings.TrimSpace(string(platform)))
	switch value {
	case "github", "git hub":
		return core.PlatformGitHub
	case "gitlab", "git lab":
		return core.PlatformGitLab
	default:
		return ""
	}
}

func int32Ptr(v int) *int32 {
	if v <= 0 {
		return nil
	}
	value := int32(v)
	return &value
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.Trim(path, "/")
}

func persistMRVersionFromInput(ctx context.Context, store db.Store, mergeRequestID int64, snapshotVersion core.PlatformVersion, version ctxpkg.VersionContext) error {
	if store == nil || mergeRequestID == 0 {
		return nil
	}
	if strings.TrimSpace(version.HeadSHA) == "" {
		return nil
	}
	latest, latestErr := store.GetLatestMRVersion(ctx, mergeRequestID)
	versionID := int64(0)
	if raw := strings.TrimSpace(snapshotVersion.PlatformVersionID); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && parsed > 0 {
			versionID = parsed
		}
	}
	if versionID <= 0 && latestErr == nil && latest.GitlabVersionID > 0 {
		versionID = latest.GitlabVersionID
	}
	if latestErr == nil &&
		latest.GitlabVersionID == versionID &&
		strings.TrimSpace(latest.HeadSha) == strings.TrimSpace(version.HeadSHA) &&
		strings.TrimSpace(latest.BaseSha) == strings.TrimSpace(version.BaseSHA) &&
		strings.TrimSpace(latest.StartSha) == strings.TrimSpace(version.StartSHA) &&
		strings.TrimSpace(latest.PatchIDSha) == strings.TrimSpace(version.PatchIDSHA) {
		return nil
	}
	_, err := store.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mergeRequestID,
		GitlabVersionID: versionID,
		BaseSha:         strings.TrimSpace(version.BaseSHA),
		StartSha:        strings.TrimSpace(version.StartSHA),
		HeadSha:         strings.TrimSpace(version.HeadSHA),
		PatchIDSha:      strings.TrimSpace(version.PatchIDSHA),
	})
	return err
}

func defaultReviewRunNewStore(conn db.DBTX) db.Store { return db.New(conn) }
