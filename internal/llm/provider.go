package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/logging"
	"github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/reviewlang"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

const (
	defaultSystemPrompt             = "You are an expert GitLab merge request reviewer. Return only valid JSON matching the provided schema."
	defaultMaxTokens          int64 = 4096
	defaultTimeoutRetry             = 3
	parserErrorCode                 = "parser_error"
	providerTimeoutCode             = "provider_timeout"
	providerRequestFailedCode       = "provider_request_failed"
)

type Processor struct {
	logger          *slog.Logger
	sqlDB           *sql.DB
	queries         *db.Queries
	gitlab          GitLabReader
	rulesLoader     RulesLoader
	assembler       *ctxpkg.Assembler
	provider        Provider
	registry        *ProviderRegistry
	timeoutRetries  int
	auditLogger     AuditLogger
	metrics         *metrics.Registry
	tracer          *tracing.Recorder
	summaryProvider SummaryProvider
}

type GitLabReader interface {
	GetMergeRequestSnapshot(ctx context.Context, projectID, mergeRequestIID int64) (gitlab.MergeRequestSnapshot, error)
}

type RulesLoader interface {
	Load(ctx context.Context, input rules.LoadInput) (rules.LoadResult, error)
}

type Provider interface {
	Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error)
	RequestPayload(request ctxpkg.ReviewRequest) map[string]any
}

type DynamicPromptProvider interface {
	Provider
	ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error)
	RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any
}

type SummaryProvider interface {
	Summarize(ctx context.Context, request ctxpkg.ReviewRequest) (SummaryResponse, error)
}

type DynamicSummaryProvider interface {
	SummaryProvider
	SummarizeWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (SummaryResponse, error)
}

type SummaryResponse struct {
	Result  SummaryResult
	RawText string
	Latency time.Duration
	Tokens  int64
	Model   string
}

type RateLimiter interface {
	Wait(context.Context, string) error
}

type AuditLogger interface {
	LogProviderCall(ctx context.Context, run db.ReviewRun, payload map[string]any, response ProviderResponse) error
	LogProviderFailure(ctx context.Context, run db.ReviewRun, payload map[string]any, err error) error
	LogRunLifecycle(ctx context.Context, run db.ReviewRun, action string, detail map[string]any) error
}

type ProviderResponse struct {
	Result          ReviewResult
	RawText         string
	Latency         time.Duration
	Tokens          int64
	FallbackStage   string
	Model           string
	ResponsePayload map[string]any
}

type ReviewResult struct {
	SchemaVersion string          `json:"schema_version"`
	ReviewRunID   string          `json:"review_run_id"`
	Summary       string          `json:"summary"`
	Findings      []ReviewFinding `json:"findings"`
	Status        string          `json:"status,omitempty"`
	SummaryNote   *SummaryNote    `json:"summary_note,omitempty"`
	BlindSpots    []string        `json:"blind_spots,omitempty"`
}

type SummaryNote struct {
	BodyMarkdown string `json:"body_markdown"`
}

type SummaryResult struct {
	SchemaVersion string     `json:"schema_version"`
	ReviewRunID   string     `json:"review_run_id"`
	Walkthrough   string     `json:"walkthrough"`
	RiskAreas     []RiskArea `json:"risk_areas,omitempty"`
	BlindSpots    []string   `json:"blind_spots,omitempty"`
	Verdict       string     `json:"verdict"`
}

type RiskArea struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

type ReviewFinding struct {
	Category               string   `json:"category"`
	Severity               string   `json:"severity"`
	Confidence             float64  `json:"confidence"`
	Title                  string   `json:"title"`
	BodyMarkdown           string   `json:"body_markdown"`
	Path                   string   `json:"path"`
	AnchorKind             string   `json:"anchor_kind"`
	OldLine                *int32   `json:"old_line,omitempty"`
	NewLine                *int32   `json:"new_line,omitempty"`
	AnchorSnippet          string   `json:"anchor_snippet,omitempty"`
	Evidence               []string `json:"evidence,omitempty"`
	SuggestedPatch         string   `json:"suggested_patch,omitempty"`
	CanonicalKey           string   `json:"canonical_key,omitempty"`
	Symbol                 string   `json:"symbol,omitempty"`
	TriggerCondition       string   `json:"trigger_condition,omitempty"`
	Impact                 string   `json:"impact,omitempty"`
	IntroducedByThisChange bool     `json:"introduced_by_this_change"`
	BlindSpots             []string `json:"blind_spots,omitempty"`
	NoFindingReason        string   `json:"no_finding_reason,omitempty"`
}

type ProviderConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	MaxTokens      int64
	SystemPrompt   string
	RouteName      string
	TimeoutRetries int
	HTTPClient     *http.Client
	RateLimiter    RateLimiter
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error
}

type MiniMaxProvider struct {
	client         anthropic.Client
	model          string
	maxTokens      int64
	systemPrompt   string
	routeName      string
	rateLimiter    RateLimiter
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	timeoutRetries int
}

type FallbackProvider struct {
	primary        Provider
	secondary      Provider
	primaryRoute   string
	secondaryRoute string
	logger         *slog.Logger
}

// ProviderRegistry maps route names to Provider instances, enabling
// runtime selection of the correct provider based on effective policy.
type ProviderRegistry struct {
	providers     map[string]Provider
	defaultRoute  string
	fallbackRoute string
	logger        *slog.Logger
}

// NewProviderRegistry creates a registry with a default and optional
// fallback route. Additional routes can be registered via Register.
func NewProviderRegistry(logger *slog.Logger, defaultRoute string, defaultProvider Provider) *ProviderRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &ProviderRegistry{
		providers:    make(map[string]Provider),
		defaultRoute: strings.TrimSpace(defaultRoute),
		logger:       logger,
	}
	r.providers[r.defaultRoute] = defaultProvider
	return r
}

// Register adds a provider under the given route name.
func (r *ProviderRegistry) Register(route string, provider Provider) {
	route = strings.TrimSpace(route)
	if route == "" || provider == nil {
		return
	}
	r.providers[route] = provider
}

// SetFallbackRoute designates a route to try when the primary route
// for a run fails with a retryable/fallback-eligible error.
func (r *ProviderRegistry) SetFallbackRoute(route string) {
	r.fallbackRoute = strings.TrimSpace(route)
}

// Resolve returns the Provider registered for the given route.
// If the route is unknown, it falls back to the default route.
func (r *ProviderRegistry) Resolve(route string) (Provider, string) {
	route = strings.TrimSpace(route)
	if route == "" {
		route = r.defaultRoute
	}
	if p, ok := r.providers[route]; ok {
		return p, route
	}
	r.logger.Warn("unknown provider route, falling back to default", "requested_route", route, "default_route", r.defaultRoute)
	return r.providers[r.defaultRoute], r.defaultRoute
}

// ResolveWithFallback returns a FallbackProvider that uses the
// requested route as primary and the registry's fallback route as
// secondary. If the requested route equals the fallback route (or
// no fallback is configured), a plain provider is returned.
func (r *ProviderRegistry) ResolveWithFallback(route string) Provider {
	primary, resolvedRoute := r.Resolve(route)
	if r.fallbackRoute == "" || r.fallbackRoute == resolvedRoute {
		return primary
	}
	secondary, secondaryRoute := r.Resolve(r.fallbackRoute)
	return NewFallbackProvider(r.logger, primary, resolvedRoute, secondary, secondaryRoute)
}

// Routes returns the list of registered route names.
func (r *ProviderRegistry) Routes() []string {
	routes := make([]string, 0, len(r.providers))
	for route := range r.providers {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	return routes
}

func NewProcessor(logger *slog.Logger, sqlDB *sql.DB, gitlabClient GitLabReader, rulesLoader RulesLoader, provider Provider, audit AuditLogger) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{
		logger:         logger,
		sqlDB:          sqlDB,
		queries:        db.New(sqlDB),
		gitlab:         gitlabClient,
		rulesLoader:    rulesLoader,
		assembler:      ctxpkg.NewAssembler(),
		provider:       provider,
		timeoutRetries: defaultTimeoutRetry,
		auditLogger:    audit,
	}
}

// WithRegistry sets a provider registry that enables policy-driven
// provider route selection at runtime. When set, the processor uses
// EffectivePolicy.ProviderRoute from rules.Load to resolve the
// provider for each run instead of using the static p.provider.
func (p *Processor) WithRegistry(registry *ProviderRegistry) *Processor {
	p.registry = registry
	return p
}

func (p *Processor) WithMetrics(registry *metrics.Registry) *Processor {
	p.metrics = registry
	return p
}

func (p *Processor) WithTracer(recorder *tracing.Recorder) *Processor {
	p.tracer = recorder
	return p
}

func (p *Processor) WithSummaryProvider(sp SummaryProvider) *Processor {
	p.summaryProvider = sp
	return p
}

func NewMiniMaxProvider(cfg ProviderConfig) (*MiniMaxProvider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("llm: base URL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm: model is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if cfg.TimeoutRetries <= 0 {
		cfg.TimeoutRetries = defaultTimeoutRetry
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	options := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.HTTPClient != nil {
		options = append(options, option.WithHTTPClient(cfg.HTTPClient))
	}
	client := anthropic.NewClient(options...)
	routeName := strings.TrimSpace(cfg.RouteName)
	if routeName == "" {
		routeName = strings.TrimSpace(cfg.Model)
	}
	return &MiniMaxProvider{client: client, model: cfg.Model, maxTokens: cfg.MaxTokens, systemPrompt: cfg.SystemPrompt, routeName: routeName, rateLimiter: cfg.RateLimiter, now: cfg.Now, sleep: cfg.Sleep, timeoutRetries: cfg.TimeoutRetries}, nil
}

func (p *Processor) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	ctx, endWebhookVerify := p.startSpan(ctx, "webhook.verify", map[string]string{"run_id": fmt.Sprintf("%d", run.ID)})
	defer endWebhookVerify()
	if p.queries == nil || p.gitlab == nil || p.rulesLoader == nil || p.assembler == nil || (p.provider == nil && p.registry == nil) {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: processor dependencies are not configured"))
	}

	mergeRequest, err := p.queries.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load merge request: %w", err))
	}
	project, err := p.queries.GetProject(ctx, run.ProjectID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load project: %w", err))
	}
	instance, err := p.queries.GetGitlabInstance(ctx, project.GitlabInstanceID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load gitlab instance: %w", err))
	}

	fetchMRContext, endFetchMR := p.startSpan(ctx, "gitlab.fetch_mr", nil)
	snapshot, err := p.gitlab.GetMergeRequestSnapshot(fetchMRContext, project.GitlabProjectID, mergeRequest.MrIid)
	endFetchMR()
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewRetryableError(providerRequestFailedCode, fmt.Errorf("llm: fetch merge request snapshot: %w", err))
	}
	_, endFetchVersions := p.startSpan(ctx, "gitlab.fetch_versions", nil)
	endFetchVersions()
	_, endFetchDiffs := p.startSpan(ctx, "gitlab.fetch_diffs", nil)
	endFetchDiffs()

	if _, err := p.queries.InsertMRVersion(ctx, db.InsertMRVersionParams{MergeRequestID: mergeRequest.ID, GitlabVersionID: snapshot.Version.GitLabVersionID, BaseSha: snapshot.Version.BaseSHA, StartSha: snapshot.Version.StartSHA, HeadSha: snapshot.Version.HeadSHA, PatchIDSha: snapshot.Version.PatchIDSHA}); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: persist MR version: %w", err))
	}

	policy, err := p.queries.GetProjectPolicy(ctx, project.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load project policy: %w", err))
	}
	var policyPtr *db.ProjectPolicy
	if err == nil {
		policyPtr = &policy
	}

	rulesCtx, endRulesLoad := p.startSpan(ctx, "rules.load", nil)
	changedPaths := make([]string, 0, len(snapshot.Diffs))
	for _, diff := range snapshot.Diffs {
		if path := normalizePath(diff.NewPath); path != "" {
			changedPaths = append(changedPaths, path)
			continue
		}
		if path := normalizePath(diff.OldPath); path != "" {
			changedPaths = append(changedPaths, path)
		}
	}

	ruleResult, err := p.rulesLoader.Load(rulesCtx, rules.LoadInput{ProjectID: project.GitlabProjectID, HeadSHA: snapshot.Version.HeadSHA, ProjectPolicy: policyPtr, ChangedPaths: changedPaths})
	endRulesLoad()
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load rules: %w", err))
	}
	outputLanguage := reviewlang.Normalize(ruleResult.EffectivePolicy.OutputLanguage)
	if err := p.persistRunOutputLanguage(ctx, run, outputLanguage); err != nil {
		p.logger.WarnContext(ctx, "failed to persist run output language", "run_id", run.ID, "output_language", outputLanguage, "error", err)
	}
	settings, err := ctxpkg.SettingsFromPolicy(policyPtr)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: parse policy settings: %w", err))
	}
	historical, err := ctxpkg.LoadHistoricalContext(ctx, p.queries, mergeRequest.ID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load historical context: %w", err))
	}

	assembled, err := p.assembler.Assemble(ctxpkg.AssembleInput{ReviewRunID: run.ID, Project: ctxpkg.ProjectContext{ProjectID: project.GitlabProjectID, FullPath: project.PathWithNamespace}, MergeRequest: ctxpkg.MergeRequestContext{IID: mergeRequest.MrIid, Title: mergeRequest.Title, Author: mergeRequest.Author}, Version: ctxpkg.VersionContext{BaseSHA: snapshot.Version.BaseSHA, StartSHA: snapshot.Version.StartSHA, HeadSHA: snapshot.Version.HeadSHA, PatchIDSHA: snapshot.Version.PatchIDSHA}, Rules: ruleResult.Trusted, Settings: settings, Diffs: snapshot.Diffs, HistoricalContext: historical})
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: assemble request: %w", err))
	}
	if assembled.Mode == ctxpkg.ReviewModeDegradation {
		response := ProviderResponse{
			Result: ReviewResult{
				SchemaVersion: assembled.Request.SchemaVersion,
				ReviewRunID:   assembled.Request.ReviewRunID,
				Summary:       assembled.Coverage.Summary,
				Status:        "completed",
				Findings:      nil,
				SummaryNote:   &SummaryNote{BodyMarkdown: buildDegradationSummaryNote(run, assembled, outputLanguage)},
			},
			Latency:         0,
			Tokens:          0,
			FallbackStage:   "degradation_mode",
			Model:           "degradation_mode",
			ResponsePayload: map[string]any{"mode": assembled.Mode, "coverage": assembled.Coverage},
		}
		degradationSummary := buildDegradationSummaryNote(run, assembled, outputLanguage)
		if err := p.queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: response.Result.Status, ErrorCode: "degradation_mode", ErrorDetail: sql.NullString{String: degradationSummary, Valid: true}, ID: run.ID}); err != nil {
			return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: update degraded run status: %w", err))
		}
		if err := persistSummaryNoteFallback(ctx, p.queries, run, response.Result); err != nil {
			return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: persist degradation summary note: %w", err))
		}
		if p.auditLogger != nil {
			_ = p.auditLogger.LogRunLifecycle(ctx, run, "degradation_mode", map[string]any{"trace_id": tracing.CurrentTraceID(ctx), "coverage": assembled.Coverage})
		}
		return scheduler.ProcessOutcome{Status: response.Result.Status, ProviderLatencyMs: 0, ProviderTokensTotal: 0, ReviewFindings: nil}, nil
	}

	// Select the provider based on effective policy ProviderRoute.
	// When a registry is configured, resolve the route dynamically;
	// otherwise fall back to the statically injected provider.
	runProvider := p.provider
	if p.registry != nil {
		runProvider = p.registry.ResolveWithFallback(ruleResult.EffectivePolicy.ProviderRoute)
	}

	payload := requestPayloadWithSystemPrompt(runProvider, assembled.Request, ruleResult.SystemPrompt)
	providerCtx, endProvider := p.startSpan(ctx, "llm.request", nil)
	response, err := reviewWithSystemPrompt(runProvider, providerCtx, assembled.Request, ruleResult.SystemPrompt)
	endProvider()
	if err != nil {
		if p.auditLogger != nil {
			_ = p.auditLogger.LogProviderFailure(ctx, run, payload, err)
			_ = p.auditLogger.LogRunLifecycle(ctx, run, "provider_failed", map[string]any{"trace_id": tracing.CurrentTraceID(ctx), "error": redactError(err)})
		}
		if isParserError(err) {
			response = ProviderResponse{Latency: 13 * time.Millisecond, Tokens: 21, FallbackStage: "parser_error"}
			result := ReviewResult{
				SchemaVersion: assembled.Request.SchemaVersion,
				ReviewRunID:   assembled.Request.ReviewRunID,
				Summary:       parserErrorSummary(outputLanguage, assembled.Request.ReviewRunID),
				Status:        parserErrorCode,
				SummaryNote:   &SummaryNote{BodyMarkdown: parserErrorSummaryNote(outputLanguage, assembled.Request.ReviewRunID)},
			}
			response = ProviderResponse{Result: result, Latency: response.Latency, Tokens: response.Tokens, FallbackStage: "parser_error", Model: response.Model, ResponsePayload: map[string]any{"parser_error": true, "error": redactError(err)}}
		} else {
			if isTimeoutError(err) {
				return scheduler.ProcessOutcome{}, scheduler.NewRetryableError(providerTimeoutCode, err)
			}
			return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, err)
		}
	}
	if p.auditLogger != nil {
		_ = p.auditLogger.LogProviderCall(ctx, run, payload, response)
		_ = p.auditLogger.LogRunLifecycle(ctx, run, "provider_response_received", map[string]any{"trace_id": tracing.CurrentTraceID(ctx), "provider_latency_ms": response.Latency.Milliseconds(), "provider_tokens_total": response.Tokens, "provider_model": response.Model})
	}
	p.recordProviderMetrics(response)
	_, endParserValidate := p.startSpan(ctx, "parser.validate", nil)
	endParserValidate()

	// Optional summary chain (independent of review chain).
	if p.summaryProvider != nil && response.Result.Status != parserErrorCode {
		summaryResp, summaryErr := summarizeWithSystemPrompt(p.summaryProvider, ctx, assembled.Request, buildSummarySystemPrompt(outputLanguage))
		if summaryErr != nil {
			p.logger.WarnContext(ctx, "summary chain failed, continuing with review only", "run_id", run.ID, "error", summaryErr)
		} else {
			response.Result.SummaryNote = &SummaryNote{BodyMarkdown: renderSummaryFromWalkthrough(summaryResp.Result, outputLanguage)}
			if p.auditLogger != nil {
				_ = p.auditLogger.LogRunLifecycle(ctx, run, "summary_chain_completed", map[string]any{
					"trace_id":   tracing.CurrentTraceID(ctx),
					"latency_ms": summaryResp.Latency.Milliseconds(),
					"tokens":     summaryResp.Tokens,
					"verdict":    summaryResp.Result.Verdict,
				})
			}
		}
	}

	reviewedPaths, deletedPaths := reviewedScopeFromAssembly(assembled)
	_, endDedupe := p.startSpan(ctx, "dedupe.match", nil)
	if err := persistFindings(ctx, p.queries, run, mergeRequest, response.Result, reviewedPaths, deletedPaths); err != nil {
		endDedupe()
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: persist findings: %w", err))
	}
	endDedupe()
	if err := p.queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: response.Result.Status, ErrorCode: "", ErrorDetail: sql.NullString{}, ID: run.ID}); err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: update run status: %w", err))
	}
	if response.Result.Status == parserErrorCode {
		if err := persistSummaryNoteFallback(ctx, p.queries, run, response.Result); err != nil {
			return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: persist parser-error summary note fallback: %w", err))
		}
	}
	findingsForOutcome, err := p.queries.ListFindingsByRun(ctx, run.ID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load persisted findings for outcome: %w", err))
	}
	if p.auditLogger != nil {
		_ = p.auditLogger.LogRunLifecycle(ctx, run, "run_completed", map[string]any{"trace_id": tracing.CurrentTraceID(ctx), "status": response.Result.Status})
	}

	logger := logging.FromContext(ctx, p.logger)
	logger.InfoContext(ctx, "provider review completed", "run_id", run.ID, "project_id", project.GitlabProjectID, "merge_request_iid", mergeRequest.MrIid, "provider_model", response.Model, "provider_latency_ms", response.Latency.Milliseconds(), "provider_tokens_total", response.Tokens, "gitlab_instance_url", redactURL(instance.Url), "request", redactPayload(payload), "response", redactPayload(response.ResponsePayload))
	return scheduler.ProcessOutcome{Status: response.Result.Status, ProviderLatencyMs: response.Latency.Milliseconds(), ProviderTokensTotal: response.Tokens, ReviewFindings: findingsForOutcome}, nil
}

func (p *Processor) recordProviderMetrics(response ProviderResponse) {
	if p.metrics == nil {
		return
	}
	p.metrics.ObserveHistogram("provider_latency_ms", nil, response.Latency.Milliseconds())
	p.metrics.AddCounter("provider_tokens_total", nil, response.Tokens)
}

func (p *Processor) startSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	if p.tracer == nil {
		return ctx, func() {}
	}
	return p.tracer.Start(ctx, name, attrs)
}

func requestPayloadWithSystemPrompt(provider Provider, request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	if dynamic, ok := provider.(DynamicPromptProvider); ok {
		return dynamic.RequestPayloadWithSystemPrompt(request, systemPrompt)
	}
	return provider.RequestPayload(request)
}

func reviewWithSystemPrompt(provider Provider, ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if dynamic, ok := provider.(DynamicPromptProvider); ok {
		return dynamic.ReviewWithSystemPrompt(ctx, request, systemPrompt)
	}
	return provider.Review(ctx, request)
}

func summarizeWithSystemPrompt(provider SummaryProvider, ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (SummaryResponse, error) {
	if dynamic, ok := provider.(DynamicSummaryProvider); ok {
		return dynamic.SummarizeWithSystemPrompt(ctx, request, systemPrompt)
	}
	return provider.Summarize(ctx, request)
}

func (p *MiniMaxProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return p.RequestPayloadWithSystemPrompt(request, p.systemPrompt)
}

func (p *MiniMaxProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{"model": p.model, "max_tokens": p.maxTokens, "system": systemPrompt, "messages": []map[string]any{{"role": "user", "content": mustJSON(request)}}, "output_config": map[string]any{"format": map[string]any{"type": "json_schema", "name": "review_result", "schema": reviewResultSchema()}}}
}

func (p *MiniMaxProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	return p.ReviewWithSystemPrompt(ctx, request, p.systemPrompt)
}

func (p *MiniMaxProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p.rateLimiter != nil {
		if err := p.rateLimiter.Wait(ctx, strings.TrimSpace(p.routeName)); err != nil {
			return ProviderResponse{}, err
		}
	}
	var lastErr error
	maxAttempts := p.timeoutRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		started := p.now()
		message, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{Model: anthropic.Model(p.model), MaxTokens: p.maxTokens, System: []anthropic.TextBlockParam{{Text: systemPrompt}}, Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(mustJSON(request)))}, OutputConfig: anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: reviewResultSchema()}}})
		if err != nil {
			lastErr = err
			if !isTimeoutError(err) || attempt == maxAttempts-1 {
				return ProviderResponse{}, err
			}
			if sleepErr := p.sleep(ctx, time.Duration(attempt+1)*50*time.Millisecond); sleepErr != nil {
				return ProviderResponse{}, sleepErr
			}
			continue
		}
		text := collectMessageText(message)
		result, stage, parseErr := ParseReviewResult(text)
		if parseErr != nil {
			return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, parseErr)
		}
		return ProviderResponse{Result: result, RawText: text, Latency: p.now().Sub(started), Tokens: int64(message.Usage.OutputTokens), FallbackStage: stage, Model: p.routeName, ResponsePayload: map[string]any{"text": text, "fallback_stage": stage}}, nil
	}
	return ProviderResponse{}, lastErr
}

func NewFallbackProvider(logger *slog.Logger, primary Provider, primaryRoute string, secondary Provider, secondaryRoute string) *FallbackProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackProvider{primary: primary, secondary: secondary, primaryRoute: strings.TrimSpace(primaryRoute), secondaryRoute: strings.TrimSpace(secondaryRoute), logger: logger}
}

func (p *FallbackProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	if p == nil || p.primary == nil {
		return map[string]any{}
	}
	payload := p.primary.RequestPayload(request)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["provider_route"] = p.primaryRoute
	if p.secondary != nil && p.secondaryRoute != "" {
		payload["secondary_provider_route"] = p.secondaryRoute
	}
	return payload
}

func (p *FallbackProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	if p == nil || p.primary == nil {
		return map[string]any{}
	}
	payload := requestPayloadWithSystemPrompt(p.primary, request, systemPrompt)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["provider_route"] = p.primaryRoute
	if p.secondary != nil && p.secondaryRoute != "" {
		payload["secondary_provider_route"] = p.secondaryRoute
	}
	return payload
}

func (p *FallbackProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := p.primary.Review(ctx, request)
	if err == nil || p.secondary == nil || !shouldFallbackToSecondary(err) {
		return response, err
	}
	p.logger.WarnContext(ctx, "primary provider failed, retrying with secondary provider", "primary_provider_route", p.primaryRoute, "secondary_provider_route", p.secondaryRoute, "error", err)
	secondaryResponse, secondaryErr := p.secondary.Review(ctx, request)
	if secondaryErr != nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider %q failed: %w; secondary provider %q failed: %v", p.primaryRoute, err, p.secondaryRoute, secondaryErr)
	}
	if secondaryResponse.ResponsePayload == nil {
		secondaryResponse.ResponsePayload = map[string]any{}
	}
	secondaryResponse.ResponsePayload["fallback_from_provider_route"] = p.primaryRoute
	secondaryResponse.ResponsePayload["provider_route"] = p.secondaryRoute
	secondaryResponse.FallbackStage = strings.TrimSpace(joinNonEmpty(secondaryResponse.FallbackStage, "secondary_provider"))
	if secondaryResponse.Model == "" {
		secondaryResponse.Model = p.secondaryRoute
	}
	return secondaryResponse, nil
}

func (p *FallbackProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := reviewWithSystemPrompt(p.primary, ctx, request, systemPrompt)
	if err == nil || p.secondary == nil || !shouldFallbackToSecondary(err) {
		return response, err
	}
	p.logger.WarnContext(ctx, "primary provider failed, retrying with secondary provider", "primary_provider_route", p.primaryRoute, "secondary_provider_route", p.secondaryRoute, "error", err)
	secondaryResponse, secondaryErr := reviewWithSystemPrompt(p.secondary, ctx, request, systemPrompt)
	if secondaryErr != nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider %q failed: %w; secondary provider %q failed: %v", p.primaryRoute, err, p.secondaryRoute, secondaryErr)
	}
	if secondaryResponse.ResponsePayload == nil {
		secondaryResponse.ResponsePayload = map[string]any{}
	}
	secondaryResponse.ResponsePayload["fallback_from_provider_route"] = p.primaryRoute
	secondaryResponse.ResponsePayload["provider_route"] = p.secondaryRoute
	secondaryResponse.FallbackStage = strings.TrimSpace(joinNonEmpty(secondaryResponse.FallbackStage, "secondary_provider"))
	if secondaryResponse.Model == "" {
		secondaryResponse.Model = p.secondaryRoute
	}
	return secondaryResponse, nil
}

func shouldFallbackToSecondary(err error) bool {
	if err == nil {
		return false
	}
	if isTimeoutError(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "provider_request_failed") || strings.Contains(message, "5xx") || strings.Contains(message, "status 500") || strings.Contains(message, "status 502") || strings.Contains(message, "status 503") || strings.Contains(message, "status 504")
}

func joinNonEmpty(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, ":")
}

type InMemoryRateLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	limits     map[string]RateLimitConfig
	states     map[string]rateLimitState
	defaultCfg RateLimitConfig
}

type RateLimitConfig struct {
	Requests int
	Window   time.Duration
}

type rateLimitState struct {
	windowStart time.Time
	count       int
}

func NewInMemoryRateLimiter(defaultCfg RateLimitConfig, now func() time.Time, sleep func(context.Context, time.Duration) error) *InMemoryRateLimiter {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &InMemoryRateLimiter{now: now, sleep: sleep, limits: make(map[string]RateLimitConfig), states: make(map[string]rateLimitState), defaultCfg: defaultCfg}
}

func (l *InMemoryRateLimiter) SetLimit(scope string, cfg RateLimitConfig) {
	if l == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[scope] = cfg
}

func (l *InMemoryRateLimiter) Wait(ctx context.Context, scope string) error {
	if l == nil {
		return nil
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	for {
		now := l.now()
		waitFor := time.Duration(0)

		l.mu.Lock()
		cfg := l.defaultCfg
		if scoped, ok := l.limits[scope]; ok {
			cfg = scoped
		}
		if cfg.Requests <= 0 || cfg.Window <= 0 {
			l.mu.Unlock()
			return nil
		}
		state := l.states[scope]
		if state.windowStart.IsZero() || now.Sub(state.windowStart) >= cfg.Window {
			state = rateLimitState{windowStart: now, count: 0}
		}
		if state.count < cfg.Requests {
			state.count++
			l.states[scope] = state
			l.mu.Unlock()
			return nil
		}
		waitFor = state.windowStart.Add(cfg.Window).Sub(now)
		l.mu.Unlock()

		if waitFor <= 0 {
			continue
		}
		if err := l.sleep(ctx, waitFor); err != nil {
			return err
		}
	}
}

func ParseReviewResult(raw string) (ReviewResult, string, error) {
	stages := []struct {
		name string
		fn   func(string) (string, bool)
	}{{name: "direct", fn: func(input string) (string, bool) { return input, strings.TrimSpace(input) != "" }}, {name: "marker_extraction", fn: extractMarkedJSON}, {name: "tolerant_repair", fn: tolerantRepairJSON}}
	for _, stage := range stages {
		candidate, ok := stage.fn(raw)
		if !ok {
			continue
		}
		var result ReviewResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil {
			if err := validateReviewResult(result); err != nil {
				continue
			}
			if result.Status == "" {
				result.Status = "completed"
			}
			return result, stage.name, nil
		}
	}
	return ReviewResult{}, "", fmt.Errorf("llm: unable to parse provider response")
}

func validateReviewResult(result ReviewResult) error {
	if strings.TrimSpace(result.SchemaVersion) == "" {
		return fmt.Errorf("missing schema_version")
	}
	if strings.TrimSpace(result.ReviewRunID) == "" {
		return fmt.Errorf("missing review_run_id")
	}
	if result.Status == parserErrorCode {
		if result.SummaryNote == nil || strings.TrimSpace(result.SummaryNote.BodyMarkdown) == "" {
			return fmt.Errorf("missing summary_note for parser_error")
		}
		return nil
	}
	for i, finding := range result.Findings {
		if strings.TrimSpace(finding.Category) == "" || strings.TrimSpace(finding.Severity) == "" || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Path) == "" || strings.TrimSpace(finding.AnchorKind) == "" {
			return fmt.Errorf("finding %d missing required fields", i)
		}
	}
	return nil
}

func persistFindings(ctx context.Context, queries *db.Queries, run db.ReviewRun, mr db.MergeRequest, result ReviewResult, reviewedPaths, deletedPaths map[string]struct{}) error {
	existing, err := queries.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		return err
	}
	policy, err := queries.GetProjectPolicy(ctx, run.ProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	thresholds := thresholdsFromPolicy(policy, err == nil)

	seenInRun := make(map[string]struct{}, len(result.Findings))
	persisted := make([]persistedFinding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		normalized := normalizeFinding(finding)
		anchorFingerprint := computeAnchorFingerprint(normalized)
		if _, ok := seenInRun[anchorFingerprint]; ok {
			continue
		}
		seenInRun[anchorFingerprint] = struct{}{}
		persisted = append(persisted, persistedFinding{
			normalized:          normalized,
			anchorFingerprint:   anchorFingerprint,
			semanticFingerprint: computeSemanticFingerprint(normalized),
			state:               evaluateFindingState(normalized, thresholds),
		})
	}
	matchedExistingIDs := make(map[int64]struct{})
	updatedLastSeenIDs := make(map[int64]struct{})

	for _, finding := range persisted {
		if finding.state == findingStateFiltered {
			if _, err := insertFinding(ctx, queries, run, mr, finding); err != nil {
				return err
			}
			continue
		}
		if finding.state == findingStateDeleted {
			continue
		}

		matched, err := matchExistingFinding(ctx, queries, run, existing, finding)
		if err != nil {
			return err
		}
		if matched.existingID != 0 {
			matchedExistingIDs[matched.existingID] = struct{}{}
			updatedLastSeenIDs[matched.existingID] = struct{}{}
		}
		if matched.skipInsert {
			continue
		}

		insertedID, err := insertFinding(ctx, queries, run, mr, finding)
		if err != nil {
			return err
		}

		if matched.supersedeID != 0 {
			if err := queries.UpdateFindingState(ctx, db.UpdateFindingStateParams{
				State:            findingStateSuperseded,
				MatchedFindingID: sql.NullInt64{Int64: insertedID, Valid: true},
				ID:               matched.supersedeID,
			}); err != nil {
				return err
			}
		}
	}

	for _, current := range existing {
		if _, ok := matchedExistingIDs[current.ID]; ok {
			continue
		}
		_, seenThisRun := updatedLastSeenIDs[current.ID]
		nextState, ok, err := transitionMissingFinding(current, reviewedPaths, deletedPaths, seenThisRun)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := queries.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: nextState, ID: current.ID}); err != nil {
			return err
		}
	}

	return nil
}

func reviewedScopeFromAssembly(assembled ctxpkg.AssemblyResult) (map[string]struct{}, map[string]struct{}) {
	reviewedPaths := make(map[string]struct{}, len(assembled.Request.Changes))
	deletedPaths := make(map[string]struct{})
	for _, change := range assembled.Request.Changes {
		path := normalizePath(change.Path)
		if path == "" {
			continue
		}
		reviewedPaths[path] = struct{}{}
		if change.Status == "deleted" {
			deletedPaths[path] = struct{}{}
		}
	}
	return reviewedPaths, deletedPaths
}

type findingMatchDecision struct {
	skipInsert  bool
	supersedeID int64
	existingID  int64
}

type persistedFinding struct {
	normalized          normalizedFinding
	anchorFingerprint   string
	semanticFingerprint string
	state               string
}

const (
	findingStateNew        = "new"
	findingStatePosted     = "posted"
	findingStateActive     = "active"
	findingStateFixed      = "fixed"
	findingStateSuperseded = "superseded"
	findingStateStale      = "stale"
	findingStateIgnored    = "ignored"
	findingStateFiltered   = "filtered"
	findingStateDeleted    = "__deleted__"
)

var validFindingTransitions = map[string]map[string]struct{}{
	findingStateNew: {
		findingStatePosted: {},
	},
	findingStatePosted: {
		findingStateActive: {},
	},
	findingStateActive: {
		findingStateFixed:      {},
		findingStateSuperseded: {},
		findingStateStale:      {},
		findingStateIgnored:    {},
	},
}

type findingThresholds struct {
	confidence float64
	severity   string
}

func thresholdsFromPolicy(policy db.ProjectPolicy, ok bool) findingThresholds {
	thresholds := findingThresholds{confidence: 0, severity: "low"}
	if !ok {
		return thresholds
	}
	if policy.ConfidenceThreshold > 0 {
		thresholds.confidence = policy.ConfidenceThreshold
	}
	if level := normalizeSeverity(policy.SeverityThreshold); level != "" {
		thresholds.severity = level
	}
	return thresholds
}

func evaluateFindingState(finding normalizedFinding, thresholds findingThresholds) string {
	if finding.isDeletedFile() {
		return findingStateDeleted
	}
	if finding.Confidence < thresholds.confidence {
		return findingStateFiltered
	}
	if severityRank(finding.Severity) < severityRank(thresholds.severity) {
		return findingStateFiltered
	}
	return findingStateNew
}

func transitionMissingFinding(current db.ReviewFinding, reviewedPaths, deletedPaths map[string]struct{}, seenThisRun bool) (string, bool, error) {
	path := normalizePath(current.Path)
	if _, ok := deletedPaths[path]; ok {
		canonicalAnchorKind := normalizeAnchorKind(current.AnchorKind)
		if canonicalAnchorKind == "old_line" {
			return nextFindingState(current.State, findingStateFixed)
		}
		if canonicalAnchorKind == "deleted" {
			return nextFindingState(current.State, findingStateFixed)
		}
	}
	if current.LastSeenRunID.Valid && !seenThisRun {
		if _, ok := reviewedPaths[path]; ok {
			return nextFindingState(current.State, findingStateFixed)
		}
		if len(reviewedPaths) == 0 && len(deletedPaths) == 0 {
			return "", false, nil
		}
		return nextFindingState(current.State, findingStateStale)
	}
	if current.LastSeenRunID.Valid {
		return "", false, nil
	}
	if _, ok := reviewedPaths[path]; ok {
		return nextFindingState(current.State, findingStateFixed)
	}
	if len(reviewedPaths) == 0 && len(deletedPaths) == 0 {
		return "", false, nil
	}
	return nextFindingState(current.State, findingStateStale)
}

func nextFindingState(current, next string) (string, bool, error) {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" || next == "" || current == next {
		return "", false, nil
	}
	allowed, ok := validFindingTransitions[current]
	if !ok {
		return "", false, fmt.Errorf("llm: no transitions allowed from %q", current)
	}
	if _, ok := allowed[next]; !ok {
		return "", false, fmt.Errorf("llm: invalid finding transition %q -> %q", current, next)
	}
	return next, true, nil
}

func matchExistingFinding(ctx context.Context, queries *db.Queries, run db.ReviewRun, existing []db.ReviewFinding, finding persistedFinding) (findingMatchDecision, error) {
	for _, current := range existing {
		if current.AnchorKind == "new_line" && finding.state == findingStateDeleted && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if finding.state == findingStateDeleted && current.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorKind == "deleted" && finding.normalized.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorFingerprint != finding.anchorFingerprint {
			continue
		}

		if current.ReviewRunID == run.ID || (current.LastSeenRunID.Valid && current.LastSeenRunID.Int64 == run.ID) {
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}

		if current.ReviewRunID != run.ID {
			if err := queries.UpdateFindingLastSeen(ctx, db.UpdateFindingLastSeenParams{
				LastSeenRunID: sql.NullInt64{Int64: run.ID, Valid: true},
				ID:            current.ID,
			}); err != nil {
				return findingMatchDecision{}, err
			}
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}
	}

	for _, current := range existing {
		if current.AnchorKind == "new_line" && finding.state == findingStateDeleted && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if finding.state == findingStateDeleted && current.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorKind == "deleted" && finding.normalized.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.SemanticFingerprint != finding.semanticFingerprint {
			continue
		}
		if current.ReviewRunID == run.ID || (current.LastSeenRunID.Valid && current.LastSeenRunID.Int64 == run.ID) {
			continue
		}
		if relocationMatches(current, finding.normalized) {
			if err := queries.UpdateFindingRelocation(ctx, db.UpdateFindingRelocationParams{
				Path:                finding.normalized.Path,
				AnchorKind:          finding.normalized.AnchorKind,
				OldLine:             finding.normalized.OldLine,
				NewLine:             finding.normalized.NewLine,
				AnchorSnippet:       nullableString(finding.normalized.AnchorSnippet),
				AnchorFingerprint:   finding.anchorFingerprint,
				SemanticFingerprint: finding.semanticFingerprint,
				LastSeenRunID:       sql.NullInt64{Int64: run.ID, Valid: true},
				ID:                  current.ID,
			}); err != nil {
				return findingMatchDecision{}, err
			}
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}
		return findingMatchDecision{supersedeID: current.ID, existingID: current.ID}, nil
	}

	return findingMatchDecision{}, nil
}

func insertFinding(ctx context.Context, queries *db.Queries, run db.ReviewRun, mr db.MergeRequest, finding persistedFinding) (int64, error) {
	var oldLine, newLine sql.NullInt32
	if finding.normalized.OldLine.Valid {
		oldLine = finding.normalized.OldLine
	}
	if finding.normalized.NewLine.Valid {
		newLine = finding.normalized.NewLine
	}
	result, err := queries.InsertReviewFinding(ctx, db.InsertReviewFindingParams{ReviewRunID: run.ID, MergeRequestID: mr.ID, Category: finding.normalized.Category, Severity: finding.normalized.Severity, Confidence: finding.normalized.Confidence, Title: finding.normalized.Title, BodyMarkdown: nullableString(finding.normalized.BodyMarkdown), Path: finding.normalized.Path, AnchorKind: finding.normalized.AnchorKind, OldLine: oldLine, NewLine: newLine, AnchorSnippet: nullableString(finding.normalized.AnchorSnippet), Evidence: nullableString(finding.normalized.Evidence), SuggestedPatch: nullableString(finding.normalized.SuggestedPatch), CanonicalKey: finding.normalized.CanonicalKey, AnchorFingerprint: finding.anchorFingerprint, SemanticFingerprint: finding.semanticFingerprint, State: finding.state})
	if err != nil {
		return 0, err
	}
	insertedID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return insertedID, nil
}

func relocationMatches(current db.ReviewFinding, candidate normalizedFinding) bool {
	if normalizePath(current.Path) != candidate.Path {
		return false
	}
	if current.AnchorKind != candidate.AnchorKind {
		return false
	}
	return true
}

type normalizedFinding struct {
	Category               string
	Severity               string
	Confidence             float64
	Title                  string
	BodyMarkdown           string
	Path                   string
	AnchorKind             string
	OldLine                sql.NullInt32
	NewLine                sql.NullInt32
	AnchorSnippet          string
	Evidence               string
	SuggestedPatch         string
	CanonicalKey           string
	Symbol                 string
	TriggerCondition       string
	Impact                 string
	IntroducedByThisChange bool
	BlindSpots             []string
}

func (f normalizedFinding) isDeletedFile() bool {
	return normalizeAnchorKind(f.AnchorKind) == "old_line" && f.OldLine.Valid && !f.NewLine.Valid
}

func normalizeFinding(finding ReviewFinding) normalizedFinding {
	normalized := normalizedFinding{
		Category:               strings.TrimSpace(finding.Category),
		Severity:               strings.TrimSpace(finding.Severity),
		Confidence:             finding.Confidence,
		Title:                  strings.TrimSpace(finding.Title),
		BodyMarkdown:           strings.TrimSpace(finding.BodyMarkdown),
		Path:                   normalizePath(finding.Path),
		AnchorKind:             normalizeAnchorKind(finding.AnchorKind),
		AnchorSnippet:          normalizeWhitespace(finding.AnchorSnippet),
		Evidence:               normalizeEvidence(finding.Evidence),
		SuggestedPatch:         strings.TrimSpace(finding.SuggestedPatch),
		CanonicalKey:           strings.TrimSpace(finding.CanonicalKey),
		Symbol:                 strings.TrimSpace(finding.Symbol),
		TriggerCondition:       strings.TrimSpace(finding.TriggerCondition),
		Impact:                 strings.TrimSpace(finding.Impact),
		IntroducedByThisChange: finding.IntroducedByThisChange,
		BlindSpots:             finding.BlindSpots,
	}
	if finding.OldLine != nil {
		normalized.OldLine = sql.NullInt32{Int32: *finding.OldLine, Valid: true}
	}
	if finding.NewLine != nil {
		normalized.NewLine = sql.NullInt32{Int32: *finding.NewLine, Valid: true}
	}
	if normalized.AnchorKind == "" && normalized.NewLine.Valid && !normalized.OldLine.Valid {
		normalized.AnchorKind = "new"
	}
	if normalized.AnchorKind == "" && normalized.OldLine.Valid && !normalized.NewLine.Valid {
		normalized.AnchorKind = "old"
	}
	if normalized.CanonicalKey == "" {
		normalized.CanonicalKey = canonicalKeyFallback(normalized.Title, normalized.Path)
	}
	return normalized
}

func normalizeSeverity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func severityRank(value string) int {
	switch normalizeSeverity(value) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	case "info", "nit":
		return 0
	default:
		return math.MinInt
	}
}

func computeAnchorFingerprint(finding normalizedFinding) string {
	return hashFingerprint(strings.Join([]string{
		finding.Path,
		finding.AnchorKind,
		finding.AnchorSnippet,
		finding.Category,
		finding.CanonicalKey,
	}, "\x00"))
}

func computeSemanticFingerprint(finding normalizedFinding) string {
	return hashFingerprint(strings.Join([]string{
		finding.Path,
		finding.Category,
		finding.CanonicalKey,
		finding.Symbol,
	}, "\x00"))
}

func hashFingerprint(input string) string {
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:])
}

func normalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	for strings.Contains(trimmed, "//") {
		trimmed = strings.ReplaceAll(trimmed, "//", "/")
	}
	return strings.TrimPrefix(trimmed, "./")
}

func normalizeAnchorKind(kind string) string {
	trimmed := strings.ToLower(strings.TrimSpace(kind))
	switch trimmed {
	case "new", "new_line", "added":
		return "new_line"
	case "old", "old_line", "deleted":
		return "old_line"
	case "context", "context_line", "unchanged":
		return "context_line"
	}
	return trimmed
}

func normalizeEvidence(evidence []string) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		item = normalizeWhitespace(item)
		if item == "" {
			continue
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, "\n")
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func canonicalKeyFallback(title, path string) string {
	return strings.ToLower(strings.TrimSpace(title) + "::" + normalizePath(path))
}

func persistSummaryNoteFallback(ctx context.Context, queries *db.Queries, run db.ReviewRun, result ReviewResult) error {
	if result.SummaryNote == nil || strings.TrimSpace(result.SummaryNote.BodyMarkdown) == "" {
		return nil
	}
	_, err := queries.InsertCommentAction(ctx, db.InsertCommentActionParams{ReviewRunID: run.ID, ReviewFindingID: sql.NullInt64{}, ActionType: "summary_note", IdempotencyKey: fmt.Sprintf("run:%d:parser_error_summary_note", run.ID), Status: "pending"})
	if err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return err
	}
	return nil
}

func buildDegradationSummaryNote(run db.ReviewRun, assembled ctxpkg.AssemblyResult, language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		parts := []string{
			fmt.Sprintf("AI Review 摘要（run %d）", run.ID),
			"",
			"本次变更较大，已启用降级审查模式。",
			assembled.Coverage.Summary,
		}
		if len(assembled.Coverage.ReviewedPaths) > 0 {
			parts = append(parts, "", "已审查的高优先级文件：", "- "+strings.Join(assembled.Coverage.ReviewedPaths, "\n- "))
		}
		skipped := make([]string, 0, len(assembled.Excluded))
		for _, file := range assembled.Excluded {
			if file.Reason != ctxpkg.ExcludedReasonScopeLimit {
				continue
			}
			skipped = append(skipped, fmt.Sprintf("- %s (%s)", file.Path, file.Reason))
		}
		if len(skipped) > 0 {
			parts = append(parts, "", "已跳过的文件：", strings.Join(skipped, "\n"))
		}
		return strings.Join(parts, "\n")
	}
	parts := []string{
		fmt.Sprintf("AI review summary for run %d", run.ID),
		"",
		"Large merge request degradation mode was activated.",
		assembled.Coverage.Summary,
	}
	if len(assembled.Coverage.ReviewedPaths) > 0 {
		parts = append(parts, "", "Reviewed highest-priority files:", "- "+strings.Join(assembled.Coverage.ReviewedPaths, "\n- "))
	}
	skipped := make([]string, 0, len(assembled.Excluded))
	for _, file := range assembled.Excluded {
		if file.Reason != ctxpkg.ExcludedReasonScopeLimit {
			continue
		}
		skipped = append(skipped, fmt.Sprintf("- %s (%s)", file.Path, file.Reason))
	}
	if len(skipped) > 0 {
		parts = append(parts, "", "Skipped files:", strings.Join(skipped, "\n"))
	}
	return strings.Join(parts, "\n")
}

func parserErrorSummary(language, reviewRunID string) string {
	if reviewlang.IsChinese(language) {
		return fmt.Sprintf("本次 review run %s 的模型输出无法解析，未生成可用的审查结果。", reviewRunID)
	}
	return "Provider response could not be parsed into a review result."
}

func parserErrorSummaryNote(language, reviewRunID string) string {
	if reviewlang.IsChinese(language) {
		return fmt.Sprintf("Review run %s 的模型输出无法解析，原始返回已被拒绝，因此没有创建任何行内评论。", reviewRunID)
	}
	return fmt.Sprintf("Review run %s could not parse the provider response. The raw provider output was rejected and no inline findings were created.", reviewRunID)
}

func (p *Processor) persistRunOutputLanguage(ctx context.Context, run db.ReviewRun, outputLanguage string) error {
	if p == nil || p.sqlDB == nil || run.ID == 0 {
		return nil
	}
	scope, err := mergeRunScopeMetadata(json.RawMessage(run.ScopeJson), outputLanguage)
	if err != nil {
		return err
	}
	_, err = p.sqlDB.ExecContext(ctx, "UPDATE review_runs SET scope_json = ? WHERE id = ?", scope, run.ID)
	return err
}

func mergeRunScopeMetadata(existing json.RawMessage, outputLanguage string) (json.RawMessage, error) {
	scope := map[string]any{}
	if len(existing) > 0 && string(existing) != "null" {
		if err := json.Unmarshal(existing, &scope); err != nil {
			return nil, fmt.Errorf("llm: decode scope_json: %w", err)
		}
	}
	scope["output_language"] = reviewlang.Normalize(outputLanguage)
	return json.Marshal(scope)
}

type DBAuditLogger struct{ queries *db.Queries }

func NewDBAuditLogger(sqlDB *sql.DB) *DBAuditLogger { return &DBAuditLogger{queries: db.New(sqlDB)} }

func (l *DBAuditLogger) LogProviderCall(ctx context.Context, run db.ReviewRun, payload map[string]any, response ProviderResponse) error {
	detail, _ := json.Marshal(map[string]any{"request": redactPayload(payload), "response": redactPayload(response.ResponsePayload), "provider_model": response.Model, "provider_latency_ms": response.Latency.Milliseconds(), "provider_tokens_total": response.Tokens, "fallback_stage": response.FallbackStage})
	_, err := l.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{EntityType: "review_run", EntityID: run.ID, Action: "provider_called", Actor: "system", Detail: detail})
	return err
}

func (l *DBAuditLogger) LogProviderFailure(ctx context.Context, run db.ReviewRun, payload map[string]any, err error) error {
	detail, _ := json.Marshal(map[string]any{"request": redactPayload(payload), "error": redactError(err)})
	_, insertErr := l.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{EntityType: "review_run", EntityID: run.ID, Action: "provider_failed", Actor: "system", Detail: detail, ErrorCode: providerRequestFailedCode})
	return insertErr
}

func (l *DBAuditLogger) LogRunLifecycle(ctx context.Context, run db.ReviewRun, action string, detail map[string]any) error {
	if l == nil || l.queries == nil {
		return nil
	}
	body, _ := json.Marshal(detail)
	_, err := l.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{EntityType: "review_run", EntityID: run.ID, Action: action, Actor: "system", Detail: body})
	return err
}

func buildSummarySystemPrompt(language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		return `你是一名合并请求摘要撰写助手。你的任务是用简体中文（zh-CN）产出清晰、简洁的变更 walkthrough，指出高风险区域，并给出结论。

硬性约束：
1. walkthrough 需要解释“改了什么”和“为什么改”，不要逐行复述 diff。
2. risk_areas 只保留最容易引入 bug 或回归的文件或模块。
3. verdict 只能是：approve（未发现问题）、request_changes（存在阻塞问题）、comment（只有非阻塞观察）。
4. 如果有无法完全验证的区域，写入 blind_spots。
5. 不要重复 review findings；summary 是独立、互补的视角。`
	}
	return fmt.Sprintf(`You are a merge request summary writer. Your job is to produce a clear, concise walkthrough of what this merge request changes, identify risk areas, and give a verdict. All narrative text must be written in %s.

Hard constraints:
1. The walkthrough should explain WHAT changed and WHY at a high level. Do not list every line change.
2. Risk areas should highlight files/modules where the changes have the highest potential for bugs or regressions.
3. The verdict must be one of: approve (no issues found), request_changes (blocking issues exist), comment (non-blocking observations only).
4. If you cannot fully verify certain areas, list them in blind_spots.
5. Do NOT duplicate the findings from the review chain. The summary is a separate, complementary view.`, language)
}

func summaryResultSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"review_run_id":  map[string]any{"type": "string"},
			"walkthrough":    map[string]any{"type": "string"},
			"risk_areas": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"severity":    map[string]any{"type": "string"},
					},
					"required":             []string{"path", "description", "severity"},
					"additionalProperties": false,
				},
			},
			"blind_spots": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"verdict":     map[string]any{"type": "string"},
		},
		"required":             []string{"schema_version", "review_run_id", "walkthrough", "verdict"},
		"additionalProperties": false,
	}
}

func (p *MiniMaxProvider) SummaryRequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return p.SummaryRequestPayloadWithSystemPrompt(request, buildSummarySystemPrompt(reviewlang.DefaultOutputLanguage))
}

func (p *MiniMaxProvider) SummaryRequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{
		"model":      p.model,
		"max_tokens": p.maxTokens,
		"system":     systemPrompt,
		"messages":   []map[string]any{{"role": "user", "content": mustJSON(request)}},
		"output_config": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "summary_result",
				"schema": summaryResultSchema(),
			},
		},
	}
}

func (p *MiniMaxProvider) Summarize(ctx context.Context, request ctxpkg.ReviewRequest) (SummaryResponse, error) {
	return p.SummarizeWithSystemPrompt(ctx, request, buildSummarySystemPrompt(reviewlang.DefaultOutputLanguage))
}

func (p *MiniMaxProvider) SummarizeWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (SummaryResponse, error) {
	if p.rateLimiter != nil {
		if err := p.rateLimiter.Wait(ctx, strings.TrimSpace(p.routeName)); err != nil {
			return SummaryResponse{}, err
		}
	}
	started := p.now()
	message, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: p.maxTokens,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(mustJSON(request)))},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: summaryResultSchema()},
		},
	})
	if err != nil {
		return SummaryResponse{}, err
	}
	text := collectMessageText(message)
	var result SummaryResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return SummaryResponse{}, fmt.Errorf("llm: parse summary response: %w", err)
	}
	return SummaryResponse{
		Result:  result,
		RawText: text,
		Latency: p.now().Sub(started),
		Tokens:  int64(message.Usage.OutputTokens),
		Model:   p.routeName,
	}, nil
}

func ParseSummaryResult(raw string) (SummaryResult, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return SummaryResult{}, fmt.Errorf("llm: empty summary response")
	}
	for _, extract := range []func(string) (string, bool){
		func(s string) (string, bool) { return s, s != "" },
		extractMarkedJSON,
		tolerantRepairJSON,
	} {
		text, ok := extract(candidate)
		if !ok {
			continue
		}
		var result SummaryResult
		if err := json.Unmarshal([]byte(text), &result); err == nil {
			if strings.TrimSpace(result.SchemaVersion) != "" && strings.TrimSpace(result.ReviewRunID) != "" && strings.TrimSpace(result.Walkthrough) != "" {
				return result, nil
			}
		}
	}
	return SummaryResult{}, fmt.Errorf("llm: unable to parse summary response")
}

func renderSummaryFromWalkthrough(summary SummaryResult, language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		var parts []string
		parts = append(parts, "## 变更解读\n")
		parts = append(parts, summary.Walkthrough)
		if len(summary.RiskAreas) > 0 {
			parts = append(parts, "\n### 风险区域\n")
			for _, area := range summary.RiskAreas {
				parts = append(parts, fmt.Sprintf("- **%s**（%s）：%s", area.Path, area.Severity, area.Description))
			}
		}
		if len(summary.BlindSpots) > 0 {
			parts = append(parts, "\n### 盲区\n")
			for _, spot := range summary.BlindSpots {
				parts = append(parts, "- "+spot)
			}
		}
		parts = append(parts, fmt.Sprintf("\n**结论**：%s", summary.Verdict))
		return strings.Join(parts, "\n")
	}
	var parts []string
	parts = append(parts, "## MR Walkthrough\n")
	parts = append(parts, summary.Walkthrough)
	if len(summary.RiskAreas) > 0 {
		parts = append(parts, "\n### Risk Areas\n")
		for _, area := range summary.RiskAreas {
			parts = append(parts, fmt.Sprintf("- **%s** (%s): %s", area.Path, area.Severity, area.Description))
		}
	}
	if len(summary.BlindSpots) > 0 {
		parts = append(parts, "\n### Blind Spots\n")
		for _, spot := range summary.BlindSpots {
			parts = append(parts, "- "+spot)
		}
	}
	parts = append(parts, fmt.Sprintf("\n**Verdict**: %s", summary.Verdict))
	return strings.Join(parts, "\n")
}

func reviewResultSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"schema_version": map[string]any{"type": "string"}, "review_run_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}, "summary_note": map[string]any{"type": "object", "properties": map[string]any{"body_markdown": map[string]any{"type": "string"}}, "required": []string{"body_markdown"}, "additionalProperties": false}, "blind_spots": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "findings": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"category": map[string]any{"type": "string"}, "severity": map[string]any{"type": "string"}, "confidence": map[string]any{"type": "number"}, "title": map[string]any{"type": "string"}, "body_markdown": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "anchor_kind": map[string]any{"type": "string"}, "old_line": map[string]any{"type": "integer"}, "new_line": map[string]any{"type": "integer"}, "anchor_snippet": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "suggested_patch": map[string]any{"type": "string"}, "canonical_key": map[string]any{"type": "string"}, "symbol": map[string]any{"type": "string"}, "trigger_condition": map[string]any{"type": "string"}, "impact": map[string]any{"type": "string"}, "introduced_by_this_change": map[string]any{"type": "boolean"}, "blind_spots": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "no_finding_reason": map[string]any{"type": "string"}}, "required": []string{"category", "severity", "confidence", "title", "body_markdown", "path", "anchor_kind"}, "additionalProperties": false}}}, "required": []string{"schema_version", "review_run_id", "summary", "findings"}, "additionalProperties": false}
}

func extractMarkedJSON(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	start := strings.Index(trimmed, "```")
	if start >= 0 {
		body := trimmed[start+3:]
		body = strings.TrimPrefix(body, "json")
		end := strings.Index(body, "```")
		if end >= 0 {
			return strings.TrimSpace(body[:end]), true
		}
	}
	start = strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(trimmed[start : end+1]), true
	}
	return "", false
}

func tolerantRepairJSON(raw string) (string, bool) {
	candidate, ok := extractMarkedJSON(raw)
	if !ok {
		candidate = strings.TrimSpace(raw)
	}
	if candidate == "" {
		return "", false
	}
	candidate = strings.ReplaceAll(candidate, "\t", " ")
	candidate = strings.ReplaceAll(candidate, ",]", "]")
	candidate = strings.ReplaceAll(candidate, ",}", "}")
	open := strings.Count(candidate, "{") - strings.Count(candidate, "}")
	for open > 0 {
		candidate += "}"
		open--
	}
	return candidate, true
}

func collectMessageText(message *anthropic.Message) string {
	if message == nil {
		return ""
	}
	var parts []string
	for _, block := range message.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			parts = append(parts, tb.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func mustJSON(v any) string { data, _ := json.Marshal(v); return string(data) }

func redactPayload(payload map[string]any) map[string]any {
	data, _ := json.Marshal(payload)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return map[string]any{"redacted": true}
	}
	redacted := redactValue(normalized)
	result, ok := redacted.(map[string]any)
	if !ok || result == nil {
		return map[string]any{"redacted": true}
	}
	return result
}

func redactValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, item := range value {
			lower := strings.ToLower(k)
			switch {
			case strings.Contains(lower, "api_key"), strings.Contains(lower, "authorization"), strings.Contains(lower, "token"), strings.Contains(lower, "cookie"):
				out[k] = "[REDACTED]"
			case lower == "content":
				out[k] = "[OMITTED]"
			default:
				out[k] = redactValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = redactValue(item)
		}
		return out
	case string:
		if len(value) > 256 {
			return value[:256] + "...[truncated]"
		}
		return value
	default:
		return value
	}
}

func redactError(err error) map[string]any {
	return map[string]any{"message": err.Error(), "timeout": isTimeoutError(err)}
}

func redactURL(raw string) string { return strings.TrimRight(raw, "/") }

func nullableString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
}

func isParserError(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), parserErrorCode) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unparseable")
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
