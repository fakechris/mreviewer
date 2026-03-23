package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

type AuditLogger interface {
	LogProviderCall(ctx context.Context, run db.ReviewRun, payload map[string]any, response ProviderResponse) error
	LogProviderFailure(ctx context.Context, run db.ReviewRun, payload map[string]any, err error) error
	LogRunLifecycle(ctx context.Context, run db.ReviewRun, action string, detail map[string]any) error
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

	if _, err := p.queries.InsertMRVersion(ctx, db.InsertMRVersionParams{MergeRequestID: mergeRequest.ID, GitlabVersionID: snapshot.Version.GitLabVersionID, BaseSha: snapshot.Version.BaseSHA, StartSha: snapshot.Version.StartSHA, HeadSha: snapshot.Version.HeadSHA, PatchIDSha: snapshot.Version.PatchIDSHA}); err != nil && !db.IsDuplicateKeyError(err) {
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
	if overrideRoute := providerRouteFromRunScope(run.ScopeJson); overrideRoute != "" {
		ruleResult.EffectivePolicy.ProviderRoute = overrideRoute
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
			var parseErr *providerParseError
			if errors.As(err, &parseErr) {
				response = ProviderResponse{
					Latency:       parseErr.latency,
					Tokens:        parseErr.tokens,
					FallbackStage: "parser_error",
					Model:         parseErr.model,
					ResponsePayload: map[string]any{
						"parser_error": true,
						"error":        redactError(err),
						"text":         parseErr.rawResponse,
					},
				}
			}
			result := ReviewResult{
				SchemaVersion: assembled.Request.SchemaVersion,
				ReviewRunID:   assembled.Request.ReviewRunID,
				Summary:       parserErrorSummary(outputLanguage, assembled.Request.ReviewRunID),
				Status:        parserErrorCode,
				SummaryNote:   &SummaryNote{BodyMarkdown: parserErrorSummaryNote(outputLanguage, assembled.Request.ReviewRunID)},
			}
			response = ProviderResponse{
				Result:          result,
				Latency:         response.Latency,
				Tokens:          response.Tokens,
				FallbackStage:   "parser_error",
				Model:           response.Model,
				ResponsePayload: response.ResponsePayload,
			}
			if response.ResponsePayload == nil {
				response.ResponsePayload = map[string]any{"parser_error": true, "error": redactError(err)}
			}
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
	if response.Result.Status == parserErrorCode {
		if err := persistSummaryNoteFallback(ctx, p.queries, run, response.Result); err != nil {
			return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: persist parser-error summary note fallback: %w", err))
		}
	}
	findingsForOutcome, err := p.queries.ListActiveFindingsByMR(ctx, mergeRequest.ID)
	if err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: load active findings for outcome: %w", err))
	}
	finalStatus := canonicalRunStatus(response.Result.Status, len(findingsForOutcome))
	response.Result.Status = finalStatus
	if err := p.queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: finalStatus, ErrorCode: "", ErrorDetail: sql.NullString{}, ID: run.ID}); err != nil {
		return scheduler.ProcessOutcome{}, scheduler.NewTerminalError(providerRequestFailedCode, fmt.Errorf("llm: update run status: %w", err))
	}
	if p.auditLogger != nil {
		_ = p.auditLogger.LogRunLifecycle(ctx, run, "run_completed", map[string]any{"trace_id": tracing.CurrentTraceID(ctx), "status": finalStatus})
	}

	logger := logging.FromContext(ctx, p.logger)
	logger.InfoContext(ctx, "provider review completed", "run_id", run.ID, "project_id", project.GitlabProjectID, "merge_request_iid", mergeRequest.MrIid, "provider_model", response.Model, "provider_latency_ms", response.Latency.Milliseconds(), "provider_tokens_total", response.Tokens, "gitlab_instance_url", redactURL(instance.Url), "request", redactPayload(payload), "response", redactPayload(response.ResponsePayload))
	return scheduler.ProcessOutcome{Status: finalStatus, ProviderLatencyMs: response.Latency.Milliseconds(), ProviderTokensTotal: response.Tokens, ReviewFindings: findingsForOutcome}, nil
}

func canonicalRunStatus(modelStatus string, findingCount int) string {
	status := strings.ToLower(strings.TrimSpace(modelStatus))
	if status == parserErrorCode {
		return parserErrorCode
	}
	if findingCount > 0 {
		return "requested_changes"
	}
	return "completed"
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

func providerRouteFromRunScope(raw []byte) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var scope struct {
		ProviderRoute string `json:"provider_route"`
	}
	if err := json.Unmarshal(raw, &scope); err != nil {
		return ""
	}
	return strings.TrimSpace(scope.ProviderRoute)
}

type DBAuditLogger struct{ queries *db.Queries }

func NewDBAuditLogger(sqlDB *sql.DB) *DBAuditLogger { return &DBAuditLogger{queries: db.New(sqlDB)} }

func (l *DBAuditLogger) LogProviderCall(ctx context.Context, run db.ReviewRun, payload map[string]any, response ProviderResponse) error {
	detail, _ := json.Marshal(map[string]any{"request": payload, "response": response.ResponsePayload, "provider_model": response.Model, "provider_latency_ms": response.Latency.Milliseconds(), "provider_tokens_total": response.Tokens, "fallback_stage": response.FallbackStage})
	_, err := l.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{EntityType: "review_run", EntityID: run.ID, Action: "provider_called", Actor: "system", Detail: detail})
	return err
}

func (l *DBAuditLogger) LogProviderFailure(ctx context.Context, run db.ReviewRun, payload map[string]any, err error) error {
	detailMap := map[string]any{"request": payload, "error": redactError(err)}
	var parseErr *providerParseError
	if errors.As(err, &parseErr) {
		detailMap["response"] = map[string]any{"text": parseErr.rawResponse}
		detailMap["provider_latency_ms"] = parseErr.latency.Milliseconds()
		detailMap["provider_tokens_total"] = parseErr.tokens
		if parseErr.model != "" {
			detailMap["provider_model"] = parseErr.model
		}
	}
	detail, _ := json.Marshal(detailMap)
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
