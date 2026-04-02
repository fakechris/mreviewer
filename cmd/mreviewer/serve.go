package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mreviewer/mreviewer/internal/adminapi"
	"github.com/mreviewer/mreviewer/internal/adminui"
	"github.com/mreviewer/mreviewer/internal/commands"
	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/githubhooks"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/hooks"
	apphttp "github.com/mreviewer/mreviewer/internal/http"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/logging"
	"github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/ops"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
	"github.com/mreviewer/mreviewer/internal/reviewruntime"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/runs"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	"github.com/mreviewer/mreviewer/internal/server"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
	"github.com/mreviewer/mreviewer/internal/writer"
)

type serveOptions struct {
	configPath string
	port       string
	dsn        string
	dryRun     bool
	verbose    int
}

type serveDeps struct {
	loadConfig       func(string) (*config.Config, error)
	migrateUpFromDSN func(string) error
	openWithDialect  func(string) (*sql.DB, database.Dialect, error)
	newIngressMux    func(*slog.Logger, *config.Config, *sql.DB, database.Dialect) (http.Handler, error)
	newWorker        func(*slog.Logger, *config.Config, *sql.DB, database.Dialect) (*personalWorkerRuntime, error)
	newServer        func(string, http.Handler, *slog.Logger) serverStarter
	stdout           io.Writer
	stderr           io.Writer
}

type serverStarter interface {
	Start(context.Context) error
}

type personalWorkerRuntime struct {
	scheduler         *scheduler.Service
	heartbeat         *ops.Service
	heartbeatIdentity ops.WorkerIdentity
}

func (r *personalWorkerRuntime) Run(ctx context.Context) error {
	if r == nil || r.scheduler == nil {
		return fmt.Errorf("serve: worker runtime is not configured")
	}
	if r.heartbeat != nil {
		go func() {
			_ = r.heartbeat.Run(ctx, 15*time.Second, r.heartbeatIdentity)
		}()
	}
	return r.scheduler.Run(ctx)
}

func runServeCommand(args []string, stdout io.Writer, stderr io.Writer) int {
	return runServeWithDeps(args, serveDeps{
		loadConfig:       config.Load,
		migrateUpFromDSN: database.MigrateUpFromDSN,
		openWithDialect:  database.OpenWithDialect,
		newIngressMux:    newPersonalIngressMux,
		newWorker:        newPersonalWorkerRuntime,
		newServer: func(port string, handler http.Handler, logger *slog.Logger) serverStarter {
			return server.New(port, handler, logger)
		},
		stdout: stdout,
		stderr: stderr,
	})
}

func runServeWithDeps(args []string, deps serveDeps) int {
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}
	if deps.stderr == nil {
		deps.stderr = io.Discard
	}
	opts, err := parseServeOptions(args, deps.stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	logger := logging.NewLogger(slog.LevelInfo)
	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: load config: %v\n", err)
		return 1
	}
	applyPersonalDefaults(cfg)
	if strings.TrimSpace(opts.port) != "" {
		cfg.Port = strings.TrimSpace(opts.port)
	}
	if strings.TrimSpace(opts.dsn) != "" {
		cfg.DatabaseDSN = strings.TrimSpace(opts.dsn)
		cfg.MySQLDSN = ""
	}
	if err := validateServeConfig(cfg); err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: %v\n", err)
		return 1
	}
	cliTracef(deps.stderr, opts.verbose, 2, "cli: serve config loaded (config=%s port=%s dsn=%s dry-run=%t)", opts.configPath, cfg.Port, cfg.DSN(), opts.dryRun)
	if opts.dryRun {
		_, _ = fmt.Fprintf(deps.stdout, "mreviewer serve dry-run ok\n")
		_, _ = fmt.Fprintf(deps.stdout, "  port: %s\n", cfg.Port)
		_, _ = fmt.Fprintf(deps.stdout, "  db: %s\n", cfg.DSN())
		_, _ = fmt.Fprintf(deps.stdout, "  webhooks: /webhook and /github/webhook\n")
		_, _ = fmt.Fprintf(deps.stdout, "  admin: /admin/\n")
		return 0
	}
	dsn := cfg.DSN()
	if err := ensureSQLiteParentDir(dsn); err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: %v\n", err)
		return 1
	}
	if err := deps.migrateUpFromDSN(dsn); err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: migrate: %v\n", err)
		return 1
	}
	sqlDB, dialect, err := deps.openWithDialect(dsn)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: open database: %v\n", err)
		return 1
	}
	defer sqlDB.Close()

	handler, err := deps.newIngressMux(logger, cfg, sqlDB, dialect)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: build ingress: %v\n", err)
		return 1
	}
	workerRuntime, err := deps.newWorker(logger, cfg, sqlDB, dialect)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: build worker: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		errCh <- deps.newServer(cfg.Port, apphttp.RequestIDMiddleware(logger, handler), logger).Start(ctx)
	}()
	go func() { errCh <- workerRuntime.Run(ctx) }()

	_, _ = fmt.Fprintf(deps.stdout, "mreviewer serve listening on http://127.0.0.1:%s (db=%s)\n", cfg.Port, dsn)
	_, _ = fmt.Fprintf(deps.stdout, "webhooks: /webhook and /github/webhook | admin: /admin/\n")

	select {
	case <-ctx.Done():
		return 0
	case err := <-errCh:
		if err == nil || errors.Is(err, context.Canceled) {
			return 0
		}
		_, _ = fmt.Fprintf(deps.stderr, "serve failed: %v\n", err)
		return 1
	}
}

func parseServeOptions(args []string, stderr io.Writer) (serveOptions, error) {
	cleanedArgs, commonFlags, err := extractCommonCLIFlags(args)
	if err != nil {
		return serveOptions{}, err
	}
	fs := flag.NewFlagSet("mreviewer serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := serveOptions{configPath: defaultPersonalConfigPath, verbose: commonFlags.verbose}
	setFlagSetUsage(fs, `
Usage: mreviewer serve [options]

Run the local webhook runtime and admin dashboard on a single machine.

Agent-friendly flags: --dry-run (alias: --dryrun), --verbose, -vv, -vvv, -vvvv

Examples:
  mreviewer serve
  mreviewer serve --config config.yaml --port 3200
  mreviewer serve --dry-run -vv
`)
	fs.StringVar(&opts.configPath, "config", defaultPersonalConfigPath, "Path to config file")
	fs.StringVar(&opts.port, "port", "", "Port override")
	fs.StringVar(&opts.dsn, "db", "", "Database DSN override (defaults to local SQLite)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "Validate config and print the runtime plan without starting services")
	fs.BoolVar(&opts.dryRun, "dryrun", false, "Alias for --dry-run")
	if err := fs.Parse(cleanedArgs); err != nil {
		return serveOptions{}, err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return serveOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(extra, ", "))
	}
	return opts, nil
}

func applyPersonalDefaults(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.Port) == "" {
		cfg.Port = defaultPersonalPort
	}
	if strings.TrimSpace(cfg.DatabaseDSN) == "" && strings.TrimSpace(cfg.MySQLDSN) == "" {
		cfg.DatabaseDSN = defaultPersonalSQLiteDSN()
	}
	if strings.TrimSpace(cfg.GitHubBaseURL) == "" {
		cfg.GitHubBaseURL = "https://api.github.com"
	}
}

func defaultPersonalSQLiteDSN() string {
	return "file:.mreviewer/state/mreviewer.db?_pragma=busy_timeout(5000)"
}

func ensureSQLiteParentDir(dsn string) error {
	if database.DetectDialect(dsn) != database.DialectSQLite {
		return nil
	}
	path := dsn
	switch {
	case strings.HasPrefix(strings.ToLower(path), "sqlite://"):
		path = path[len("sqlite://"):]
	case strings.HasPrefix(strings.ToLower(path), "file:"):
		path = path[len("file:"):]
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func validateServeConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}
	githubConfigured := strings.TrimSpace(cfg.GitHubToken) != ""
	gitlabTokenSet := strings.TrimSpace(cfg.GitLabToken) != ""
	gitlabBaseURLSet := strings.TrimSpace(cfg.GitLabBaseURL) != ""
	gitlabConfigured := gitlabTokenSet && gitlabBaseURLSet
	if !githubConfigured && !gitlabConfigured {
		if gitlabTokenSet && !gitlabBaseURLSet {
			return fmt.Errorf("configure at least one platform: set GITHUB_TOKEN, or both GITLAB_TOKEN and GITLAB_BASE_URL")
		}
		return fmt.Errorf("configure at least one of GITLAB_TOKEN or GITHUB_TOKEN")
	}
	if _, _, _, err := providerConfigsFromConfig(cfg); err != nil {
		return err
	}
	return nil
}

func newPersonalIngressMux(logger *slog.Logger, cfg *config.Config, sqlDB *sql.DB, dialect database.Dialect) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", apphttp.NewHealthHandler(logger, sqlDB))
	newStore := database.StoreFactory(dialect)
	eventProcessor := runs.NewService(logger, sqlDB, runs.WithStoreFactory(newStore))
	runService := reviewrun.NewService(eventProcessor, nil)

	webhookHandler := hooks.NewHandler(logger, sqlDB, cfg.GitLabWebhookSecret, runService, hooks.WithHandlerStoreFactory(newStore))
	commandProcessor := commands.NewProcessor(logger, sqlDB, commands.WithStoreFactory(newStore))
	webhookHandler.SetCommandProcessor(commandProcessor)
	mux.Handle("POST /webhook", webhookHandler)

	githubWebhookHandler := githubhooks.NewHandler(logger, sqlDB, cfg.GitHubWebhookSecret, runService, githubhooks.WithHandlerStoreFactory(newStore))
	mux.Handle("POST /github/webhook", githubWebhookHandler)

	adminSvc := adminapi.NewService(newStore(sqlDB), adminapi.WithActionStoreFactory(sqlDB, newStore))
	mux.Handle("/admin/api/", adminapi.NewHandler(adminSvc, cfg.AdminToken))
	mux.Handle("/admin/", adminui.NewHandler(cfg.AdminToken))
	return mux, nil
}

func newPersonalWorkerRuntime(logger *slog.Logger, cfg *config.Config, sqlDB *sql.DB, dialect database.Dialect) (*personalWorkerRuntime, error) {
	var gitlabClient *gitlab.Client
	var githubClient *platformgithub.Client
	var err error
	if strings.TrimSpace(cfg.GitLabToken) != "" {
		if strings.TrimSpace(cfg.GitLabBaseURL) == "" {
			logger.Warn("gitlab token configured without gitlab_base_url; skipping gitlab client")
		} else {
			gitlabClient, err = gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
			if err != nil {
				return nil, err
			}
		}
	}
	repositoryRulesClient := &workerRepositoryRulesClient{gitlab: gitlabClient}
	if strings.TrimSpace(cfg.GitHubToken) != "" {
		githubClient, err = platformgithub.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken)
		if err != nil {
			return nil, err
		}
		repositoryRulesClient.github = githubClient
	}
	defaultRoute, configuredFallbackRoutes, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	rulesLoader := rules.NewLoader(repositoryRulesClient, rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       defaultRoute,
	})
	llmLimiter := llm.NewInMemoryRateLimiter(llm.RateLimitConfig{Requests: 2, Window: time.Second}, time.Now, nil)
	for route, providerCfg := range providerConfigs {
		llmLimiter.SetLimit(route, llm.RateLimitConfig{Requests: 2, Window: time.Second})
		providerCfg.RateLimiter = llmLimiter
		providerConfigs[route] = providerCfg
	}
	providerRegistry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, configuredFallbackRoutes, providerConfigs)
	if err != nil {
		return nil, err
	}
	newStore := database.StoreFactory(dialect)
	processor, err := newServeReviewRunProcessor(cfg, sqlDB, gitlabClient, githubClient, rulesLoader, providerRegistry)
	if err != nil {
		return nil, err
	}
	runService := reviewrun.NewService(nil, processor)
	statusPublisher := newServeWorkerStatusPublisher(gitlabClient, githubClient, newStore(sqlDB))
	runtime := newServeRuntime(logger, sqlDB, runService, gitlabClient, githubClient, statusPublisher, gate.NoopCIGatePublisher{}, newStore)
	return &personalWorkerRuntime{
		scheduler:         runtime.Scheduler,
		heartbeat:         runtime.Heartbeat,
		heartbeatIdentity: runtime.HeartbeatIdentity,
	}, nil
}

type serveRuntimeDeps struct {
	Scheduler         *scheduler.Service
	Heartbeat         *ops.Service
	HeartbeatIdentity ops.WorkerIdentity
}

func newServeRuntime(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient, githubClient platformgithub.PublishClient, status gate.StatusPublisher, ci gate.CIGatePublisher, newStore func(db.DBTX) db.Store) serveRuntimeDeps {
	registry := metrics.NewRegistry()
	tracer := tracing.NewRecorder()
	if configurable, ok := processor.(interface {
		WithMetrics(*metrics.Registry)
		WithTracer(*tracing.Recorder)
	}); ok {
		configurable.WithMetrics(registry)
		configurable.WithTracer(tracer)
	}
	var writeback runtimeWriteback
	if sqlDB != nil {
		writeback = newServePlatformRuntimeWriteback(sqlDB, discussionClient, githubClient, registry, tracer, newStore)
	}
	processor = wrapServeProcessorWithWriteback(sqlDB, processor, writeback, newStore)
	var auditLogger gate.AuditLogger
	if sqlDB != nil {
		auditLogger = gate.NewDBAuditLogger(newStore(sqlDB))
	}
	gateSvc := gate.NewService(gate.NoopStatusPublisher{}, ci, auditLogger)
	worker := scheduler.NewService(logger, sqlDB, processor,
		scheduler.WithMetrics(registry),
		scheduler.WithTracer(tracer),
		scheduler.WithStatusPublisher(status),
		scheduler.WithGateService(gateSvc),
		scheduler.WithStoreFactory(newStore),
	)
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "mreviewer-serve"
	}
	heartbeatIdentity := ops.WorkerIdentity{
		WorkerID:              worker.WorkerID(),
		Hostname:              hostname,
		Version:               "dev",
		ConfiguredConcurrency: int32(worker.ConfiguredConcurrency()),
	}
	var heartbeatSvc *ops.Service
	if sqlDB != nil {
		heartbeatSvc = ops.NewService(newStore(sqlDB))
	}
	return serveRuntimeDeps{
		Scheduler:         worker,
		Heartbeat:         heartbeatSvc,
		HeartbeatIdentity: heartbeatIdentity,
	}
}

type runtimeWriteback interface {
	Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error
}

type runtimeBundleWriteback interface {
	WriteBundle(ctx context.Context, run db.ReviewRun, bundle core.ReviewBundle) error
}

type servePlatformRuntimeWriteback struct {
	gitlab *platformgitlab.RuntimeWriteback
	github *platformgithub.RuntimeWriteback
}

func newServePlatformRuntimeWriteback(sqlDB *sql.DB, gitlabClient writer.DiscussionClient, githubClient platformgithub.PublishClient, registry *metrics.Registry, tracer *tracing.Recorder, newStore func(db.DBTX) db.Store) runtimeWriteback {
	router := &servePlatformRuntimeWriteback{}
	if gitlabClient != nil && sqlDB != nil {
		router.gitlab = platformgitlab.NewRuntimeWriteback(gitlabClient, writer.NewSQLStoreWithStore(newStore(sqlDB))).WithMetrics(registry).WithTracer(tracer)
	}
	if githubClient != nil {
		router.github = platformgithub.NewRuntimeWriteback(githubClient)
	}
	if router.gitlab == nil && router.github == nil {
		return nil
	}
	return router
}

func (w *servePlatformRuntimeWriteback) Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error {
	if reviewruntime.IsGitHubRuntimeRun(run, slog.Default()) {
		if w.github == nil {
			return fmt.Errorf("no github writer configured for runtime writeback")
		}
		return w.github.Write(ctx, run, findings)
	}
	if w.gitlab == nil {
		return fmt.Errorf("no gitlab writer configured for runtime writeback")
	}
	return w.gitlab.Write(ctx, run, findings)
}

func (w *servePlatformRuntimeWriteback) WriteBundle(ctx context.Context, run db.ReviewRun, bundle core.ReviewBundle) error {
	if bundle.Target.Platform == core.PlatformGitHub || reviewruntime.IsGitHubRuntimeRun(run, slog.Default()) {
		if w.github == nil {
			return fmt.Errorf("no github writer configured for runtime writeback")
		}
		return w.github.WriteBundle(ctx, run, bundle)
	}
	if w.gitlab == nil {
		return fmt.Errorf("no gitlab writer configured for runtime writeback")
	}
	return w.gitlab.WriteBundle(ctx, run, bundle)
}

func wrapServeProcessorWithWriteback(sqlDB *sql.DB, processor scheduler.Processor, runtimeWriter runtimeWriteback, newStore func(db.DBTX) db.Store) scheduler.Processor {
	if processor == nil || sqlDB == nil || runtimeWriter == nil {
		return processor
	}
	queries := newStore(sqlDB)
	return scheduler.FuncProcessor(func(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
		outcome, err := processor.ProcessRun(ctx, run)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		runForWriteback, loadErr := queries.GetReviewRun(ctx, run.ID)
		if loadErr != nil {
			return scheduler.ProcessOutcome{}, loadErr
		}
		if runForWriteback.ID == 0 {
			runForWriteback = run
		}
		if runForWriteback.Status == "" || runForWriteback.Status == "running" || runForWriteback.Status == "pending" {
			runForWriteback.Status = outcome.Status
		}
		if bundleWriter, ok := runtimeWriter.(runtimeBundleWriteback); ok {
			if bundle, ok := outcome.ReviewBundle.(core.ReviewBundle); ok {
				return outcome, bundleWriter.WriteBundle(ctx, runForWriteback, bundle)
			}
		}
		return outcome, runtimeWriter.Write(ctx, runForWriteback, outcome.ReviewFindings)
	})
}

func newServeReviewRunProcessor(cfg *config.Config, sqlDB *sql.DB, gitlabClient *gitlab.Client, githubClient *platformgithub.Client, rulesLoader reviewinput.RulesLoader, providerRegistry *llm.ProviderRegistry) (scheduler.Processor, error) {
	builder := reviewinput.NewBuilder(rulesLoader, ctxpkg.NewAssembler(), llm.NewSQLProcessorStore(sqlDB))
	inputLoader := reviewruntime.InputLoaderFunc(func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
		input, err := buildServeReviewInput(ctx, target, gitlabClient, githubClient, builder)
		if err != nil {
			return core.ReviewInput{}, err
		}
		if trimmed := strings.TrimSpace(providerRoute); trimmed != "" {
			input.EffectivePolicy.ProviderRoute = trimmed
		}
		return input, nil
	})
	return reviewruntime.NewProcessor(cfg, sqlDB, inputLoader, providerRegistry, "serve runtime advisor failed; continuing with council result")
}

func buildServeReviewInput(ctx context.Context, target core.ReviewTarget, gitlabClient *gitlab.Client, githubClient *platformgithub.Client, builder *reviewinput.Builder) (core.ReviewInput, error) {
	var (
		snapshot core.PlatformSnapshot
		err      error
	)
	switch target.Platform {
	case core.PlatformGitHub:
		if githubClient == nil {
			return core.ReviewInput{}, fmt.Errorf("build review input: github client is required")
		}
		snapshot, err = platformgithub.NewAdapter(githubClient).FetchSnapshot(ctx, target)
	case core.PlatformGitLab:
		if gitlabClient == nil {
			return core.ReviewInput{}, fmt.Errorf("build review input: gitlab client is required")
		}
		snapshot, err = platformgitlab.NewAdapter(gitlabClient).FetchSnapshot(ctx, target)
	default:
		return core.ReviewInput{}, fmt.Errorf("unsupported platform: %s", target.Platform)
	}
	if err != nil {
		return core.ReviewInput{}, err
	}
	return builder.Build(ctx, reviewinput.BuildInput{
		Snapshot:             snapshot,
		ProjectDefaultBranch: snapshot.Change.TargetBranch,
	})
}

type workerRepositoryRulesClient struct {
	gitlab *gitlab.Client
	github *platformgithub.Client
}

func (c *workerRepositoryRulesClient) GetRepositoryFile(ctx context.Context, projectID int64, filePath, ref string) (string, error) {
	if c == nil || c.gitlab == nil {
		return "", rules.ErrNoRepositoryReader
	}
	return c.gitlab.GetRepositoryFile(ctx, projectID, filePath, ref)
}

func (c *workerRepositoryRulesClient) GetRepositoryFileByRepositoryRef(ctx context.Context, repositoryRef, filePath, ref string) (string, error) {
	if c == nil || c.github == nil {
		return "", rules.ErrNoRepositoryReader
	}
	return c.github.GetRepositoryFileByRepositoryRef(ctx, repositoryRef, filePath, ref)
}

type workerStatusPublisher struct{ publishers []gate.StatusPublisher }

func newServeWorkerStatusPublisher(gitlabClient *gitlab.Client, githubClient *platformgithub.Client, store gate.StatusStore) gate.StatusPublisher {
	publisher := &workerStatusPublisher{}
	if gitlabClient != nil {
		publisher.publishers = append(publisher.publishers, gate.NewGitLabStatusPublisher(gitlabClient, store))
	}
	if githubClient != nil {
		publisher.publishers = append(publisher.publishers, gate.NewGitHubStatusPublisher(githubStatusClient{client: githubClient}, store))
	}
	if len(publisher.publishers) == 0 {
		return gate.NoopStatusPublisher{}
	}
	return publisher
}

func (p *workerStatusPublisher) PublishStatus(ctx context.Context, result gate.Result) error {
	var errs []error
	for _, publisher := range p.publishers {
		if err := publisher.PublishStatus(ctx, result); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type githubStatusClient struct{ client *platformgithub.Client }

func (c githubStatusClient) SetCommitStatus(ctx context.Context, req gate.GitHubCommitStatusRequest) error {
	return c.client.SetCommitStatus(ctx, platformgithub.CommitStatusRequest{
		Repository:  req.Repository,
		SHA:         req.SHA,
		State:       req.State,
		Context:     req.Context,
		Description: req.Description,
		TargetURL:   req.TargetURL,
	})
}
