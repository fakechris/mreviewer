package reviewrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	db        *sql.DB
	loadInput ReviewInputLoader
	engine    ReviewEngine
	newStore  func(db.DBTX) db.Store
	writeback RunWriteback
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

	bundle, err := p.engine.Run(ctx, input, core.RunOptions{
		OutputMode:    "both",
		PublishMode:   "full-review-comments",
		RouteOverride: providerRoute,
	})
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: run review engine: %w", err)
	}
	if err := persistMRVersionFromInput(ctx, store, mr.ID, input.Request.Version); err != nil {
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

func persistMRVersionFromInput(ctx context.Context, store db.Store, mergeRequestID int64, version ctxpkg.VersionContext) error {
	if store == nil || mergeRequestID == 0 {
		return nil
	}
	if strings.TrimSpace(version.HeadSHA) == "" {
		return nil
	}
	_, err := store.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mergeRequestID,
		GitlabVersionID: 0,
		BaseSha:         strings.TrimSpace(version.BaseSHA),
		StartSha:        strings.TrimSpace(version.StartSHA),
		HeadSha:         strings.TrimSpace(version.HeadSHA),
		PatchIDSha:      strings.TrimSpace(version.PatchIDSHA),
	})
	return err
}

func defaultReviewRunNewStore(conn db.DBTX) db.Store { return db.New(conn) }
