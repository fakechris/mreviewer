package reviewruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewadvisor"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type InputLoaderFunc func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error)

func (f InputLoaderFunc) Load(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
	return f(ctx, target, providerRoute)
}

type AdvisorAwareEngine struct {
	Base               *core.Engine
	Advisor            *reviewadvisor.Advisor
	DefaultAdvisorRef  string
	AdvisorWarnMessage string
}

func (e AdvisorAwareEngine) Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	if e.Base == nil {
		return core.ReviewBundle{}, fmt.Errorf("review runtime: review engine is required")
	}
	bundle, err := e.Base.Run(ctx, input, opts)
	if err != nil {
		return core.ReviewBundle{}, err
	}
	ref := strings.TrimSpace(opts.AdvisorRoute)
	if ref == "" {
		ref = strings.TrimSpace(e.DefaultAdvisorRef)
	}
	if ref == "" || e.Advisor == nil {
		return bundle, nil
	}
	artifact, err := e.Advisor.Advise(ctx, input, bundle, ref)
	if err != nil {
		message := strings.TrimSpace(e.AdvisorWarnMessage)
		if message == "" {
			message = "review runtime advisor failed; continuing with council result"
		}
		slog.Default().WarnContext(ctx, message, "route", ref, "error", err)
		return bundle, nil
	}
	bundle.AdvisorArtifact = artifact
	return bundle, nil
}

type JudgeAdapter struct {
	Inner *judge.Engine
}

func (a JudgeAdapter) Decide(artifacts []core.ReviewerArtifact) core.JudgeDecision {
	if a.Inner == nil {
		return core.JudgeDecision{}
	}
	decision := a.Inner.Decide(artifacts)
	merged := make([]core.Finding, 0, len(decision.MergedFindings))
	for _, item := range decision.MergedFindings {
		merged = append(merged, item.Finding)
	}
	result := core.JudgeDecision{
		Verdict:        decision.Verdict,
		MergedFindings: merged,
	}
	result.Summary = JudgeSummary(result)
	return result
}

func JudgeSummary(decision core.JudgeDecision) string {
	if len(decision.MergedFindings) == 0 {
		return "No review findings detected."
	}
	lines := []string{fmt.Sprintf("Verdict: %s", decision.Verdict), "", "Findings:"}
	for _, item := range decision.MergedFindings {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = strings.TrimSpace(item.Claim)
		}
		if title == "" {
			title = "Unnamed finding"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", strings.ToUpper(strings.TrimSpace(item.Severity)), title))
	}
	return strings.Join(lines, "\n")
}

func NormalizeReviewPacks(packs []string) []string {
	if len(packs) == 0 {
		return nil
	}
	result := make([]string, 0, len(packs))
	seen := make(map[string]struct{}, len(packs))
	for _, pack := range packs {
		token := strings.ToLower(strings.TrimSpace(pack))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	return result
}

func IsGitHubRuntimeRun(run db.ReviewRun, logger *slog.Logger) bool {
	if len(run.ScopeJson) == 0 {
		return false
	}
	var scope struct {
		Platform core.Platform `json:"platform"`
	}
	if err := json.Unmarshal(run.ScopeJson, &scope); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("runtime scope json is malformed; falling back to platform inference", "error", err)
		return false
	}
	return scope.Platform == core.PlatformGitHub
}

func NewProcessor(cfg *config.Config, sqlDB *sql.DB, inputLoader reviewrun.ReviewInputLoader, providerRegistry *llm.ProviderRegistry, advisorWarnMessage string) (scheduler.Processor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("review runtime: configuration is required")
	}
	if sqlDB == nil {
		return nil, fmt.Errorf("review runtime: database is required")
	}
	if inputLoader == nil {
		return nil, fmt.Errorf("review runtime: input loader is required")
	}
	if providerRegistry == nil {
		return nil, fmt.Errorf("review runtime: provider registry is required")
	}
	defaultRoute, fallbackRoutes, _, err := config.ResolveReviewCatalog(cfg)
	if err != nil {
		return nil, fmt.Errorf("review runtime: resolve review model chain: %w", err)
	}
	resolve := func(ref string) llm.Provider {
		return config.ResolveProvider(cfg, providerRegistry, defaultRoute, fallbackRoutes, ref)
	}
	runners := make([]core.PackRunner, 0, len(reviewpack.DefaultPacks()))
	for _, pack := range reviewpack.DefaultPacks() {
		runners = append(runners, reviewpack.NewLegacyResolverRunner(pack.Contract(), resolve))
	}
	engine := AdvisorAwareEngine{
		Base:               core.NewEngine(runners, JudgeAdapter{Inner: judge.New()}),
		Advisor:            reviewadvisor.New(resolve),
		DefaultAdvisorRef:  strings.TrimSpace(cfg.Review.AdvisorChain),
		AdvisorWarnMessage: advisorWarnMessage,
	}
	return reviewrun.NewEngineProcessor(sqlDB, inputLoader, engine).
		WithDefaultReviewerPacks(NormalizeReviewPacks(cfg.Review.Packs)).
		WithDefaultAdvisorRoute(strings.TrimSpace(cfg.Review.AdvisorChain)), nil
}
