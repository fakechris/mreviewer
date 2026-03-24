package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/logging"
	"github.com/mreviewer/mreviewer/internal/manualtrigger"
)

type manualTriggerService interface {
	Trigger(ctx context.Context, input manualtrigger.TriggerInput) (manualtrigger.TriggerResult, error)
	WaitForTerminalRun(ctx context.Context, runID int64) (db.ReviewRun, error)
}

type runtimeDeps struct {
	loadConfig func(string) (*config.Config, error)
	openDB     func(string) (*sql.DB, error)
	newService func(cfg *config.Config, sqlDB *sql.DB, pollInterval time.Duration) manualTriggerService
	stdout     io.Writer
	stderr     io.Writer
}

type cliOptions struct {
	projectID     int64
	mrIID         int64
	configPath    string
	providerRoute string
	wait          bool
	waitTimeout   time.Duration
	pollInterval  time.Duration
	jsonOutput    bool
}

type jsonResponse struct {
	OK       bool             `json:"ok"`
	Waited   bool             `json:"waited"`
	Created  *createdRunJSON  `json:"created,omitempty"`
	Terminal *terminalRunJSON `json:"terminal,omitempty"`
	Error    *errorJSON       `json:"error,omitempty"`
}

type createdRunJSON struct {
	RunID          int64  `json:"run_id"`
	ProjectID      int64  `json:"project_id"`
	MRIID          int64  `json:"mr_iid"`
	HeadSHA        string `json:"head_sha"`
	IdempotencyKey string `json:"idempotency_key"`
}

type terminalRunJSON struct {
	RunID               int64  `json:"run_id"`
	Status              string `json:"status"`
	TriggerType         string `json:"trigger_type"`
	HeadSHA             string `json:"head_sha"`
	ErrorCode           string `json:"error_code"`
	RetryCount          int32  `json:"retry_count"`
	MaxRetries          int32  `json:"max_retries"`
	ProviderLatencyMs   int64  `json:"provider_latency_ms"`
	ProviderTokensTotal int64  `json:"provider_tokens_total"`
}

type errorJSON struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithDeps(args, runtimeDeps{
		loadConfig: config.Load,
		openDB:     database.Open,
		newService: newDefaultService,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	})
}

func runWithDeps(args []string, deps runtimeDeps) int {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.openDB == nil {
		deps.openDB = database.Open
	}
	if deps.newService == nil {
		deps.newService = newDefaultService
	}
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}
	if deps.stderr == nil {
		deps.stderr = io.Discard
	}

	opts, err := parseCLIOptions(args, deps.stderr)
	if err != nil {
		return 2
	}

	fail := func(stage string, err error, created *manualtrigger.TriggerResult) int {
		if opts.jsonOutput {
			payload := jsonResponse{
				OK:     false,
				Waited: opts.wait,
				Error:  &errorJSON{Stage: stage, Message: err.Error()},
			}
			if created != nil {
				payload.Created = &createdRunJSON{
					RunID:          created.RunID,
					ProjectID:      created.ProjectID,
					MRIID:          created.MRIID,
					HeadSHA:        created.HeadSHA,
					IdempotencyKey: created.IdempotencyKey,
				}
			}
			_ = writeJSONResponse(deps.stdout, payload)
			return 1
		}

		_, _ = fmt.Fprintf(deps.stderr, "manual-trigger %s failed: %v\n", stage, err)
		return 1
	}

	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return fail("config", err, nil)
	}
	if err := validateProviderRouteOverride(cfg, opts.providerRoute); err != nil {
		return fail("config", err, nil)
	}

	sqlDB, err := deps.openDB(cfg.MySQLDSN)
	if err != nil {
		return fail("database", err, nil)
	}
	if sqlDB != nil {
		defer sqlDB.Close()
	}

	svc := deps.newService(cfg, sqlDB, opts.pollInterval)
	result, err := svc.Trigger(context.Background(), manualtrigger.TriggerInput{
		ProjectID:     opts.projectID,
		MRIID:         opts.mrIID,
		ProviderRoute: strings.TrimSpace(opts.providerRoute),
	})
	if err != nil {
		return fail("create", err, nil)
	}

	created := &createdRunJSON{
		RunID:          result.RunID,
		ProjectID:      result.ProjectID,
		MRIID:          result.MRIID,
		HeadSHA:        result.HeadSHA,
		IdempotencyKey: result.IdempotencyKey,
	}

	if !opts.wait {
		if opts.jsonOutput {
			_ = writeJSONResponse(deps.stdout, jsonResponse{
				OK:      true,
				Waited:  false,
				Created: created,
			})
			return 0
		}

		_, _ = fmt.Fprintf(deps.stdout,
			"created manual review run %d for project %d MR !%d (head_sha=%s, idempotency_key=%s)\n",
			result.RunID,
			result.ProjectID,
			result.MRIID,
			result.HeadSHA,
			result.IdempotencyKey,
		)
		return 0
	}

	waitCtx := context.Background()
	var cancel context.CancelFunc
	if opts.waitTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(waitCtx, opts.waitTimeout)
		defer cancel()
	}

	run, err := svc.WaitForTerminalRun(waitCtx, result.RunID)
	if err != nil {
		return fail("wait", err, &result)
	}

	terminal := runToJSON(run)
	if opts.jsonOutput {
		_ = writeJSONResponse(deps.stdout, jsonResponse{
			OK:       runSucceeded(run.Status),
			Waited:   true,
			Created:  created,
			Terminal: &terminal,
		})
		if runSucceeded(run.Status) {
			return 0
		}
		return 1
	}

	_, _ = fmt.Fprintf(deps.stdout,
		"created manual review run %d for project %d MR !%d (head_sha=%s, idempotency_key=%s)\n",
		result.RunID,
		result.ProjectID,
		result.MRIID,
		result.HeadSHA,
		result.IdempotencyKey,
	)
	_, _ = fmt.Fprintf(deps.stdout,
		"review run %d reached terminal state: status=%s error_code=%s\n",
		run.ID,
		run.Status,
		run.ErrorCode,
	)
	if runSucceeded(run.Status) {
		return 0
	}
	return 1
}

func parseCLIOptions(args []string, stderr io.Writer) (cliOptions, error) {
	opts := cliOptions{
		configPath:   "config.yaml",
		waitTimeout:  15 * time.Minute,
		pollInterval: time.Second,
	}

	fs := flag.NewFlagSet("manual-trigger", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Int64Var(&opts.projectID, "project-id", 0, "GitLab project ID")
	fs.Int64Var(&opts.mrIID, "mr-iid", 0, "GitLab merge request IID")
	fs.StringVar(&opts.configPath, "config", "config.yaml", "Path to config file")
	fs.StringVar(&opts.providerRoute, "llm-route", "", "Use the named llm.routes entry for this manual review run only")
	fs.StringVar(&opts.providerRoute, "provider-route", "", "Alias of --llm-route")
	fs.BoolVar(&opts.wait, "wait", false, "Wait for the review run to reach a terminal state")
	fs.DurationVar(&opts.waitTimeout, "wait-timeout", 15*time.Minute, "Maximum time to wait when --wait is enabled")
	fs.DurationVar(&opts.pollInterval, "poll-interval", time.Second, "Polling interval used with --wait")
	fs.BoolVar(&opts.jsonOutput, "json", false, "Emit structured JSON output")

	llmRouteArg, llmRouteSet := rawFlagValue(args, "--llm-route")
	providerRouteArg, providerRouteSet := rawFlagValue(args, "--provider-route")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if llmRouteSet && providerRouteSet && llmRouteArg != providerRouteArg {
		_, _ = fmt.Fprintln(stderr, "--llm-route and --provider-route must match when both are provided")
		fs.Usage()
		return cliOptions{}, fmt.Errorf("conflicting provider route flags")
	}
	if len(fs.Args()) > 0 {
		_, _ = fmt.Fprintf(stderr, "manual-trigger does not accept positional arguments: %v\n", fs.Args())
		fs.Usage()
		return cliOptions{}, fmt.Errorf("unexpected positional arguments")
	}
	if opts.projectID <= 0 || opts.mrIID <= 0 {
		_, _ = fmt.Fprintln(stderr, "manual-trigger requires --project-id and --mr-iid")
		fs.Usage()
		return cliOptions{}, fmt.Errorf("missing required args")
	}
	if opts.pollInterval <= 0 {
		_, _ = fmt.Fprintln(stderr, "--poll-interval must be greater than zero")
		fs.Usage()
		return cliOptions{}, fmt.Errorf("invalid poll interval")
	}
	if opts.wait && opts.waitTimeout <= 0 {
		_, _ = fmt.Fprintln(stderr, "--wait-timeout must be greater than zero when --wait is enabled")
		fs.Usage()
		return cliOptions{}, fmt.Errorf("invalid wait timeout")
	}

	return opts, nil
}

func rawFlagValue(args []string, name string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name {
			if i+1 >= len(args) {
				return "", true
			}
			return args[i+1], true
		}
		prefix := name + "="
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix), true
		}
	}
	return "", false
}

func runSucceeded(status string) bool {
	return status == "completed" || status == "parser_error"
}

func runToJSON(run db.ReviewRun) terminalRunJSON {
	return terminalRunJSON{
		RunID:               run.ID,
		Status:              run.Status,
		TriggerType:         run.TriggerType,
		HeadSHA:             run.HeadSha,
		ErrorCode:           run.ErrorCode,
		RetryCount:          run.RetryCount,
		MaxRetries:          run.MaxRetries,
		ProviderLatencyMs:   run.ProviderLatencyMs,
		ProviderTokensTotal: run.ProviderTokensTotal,
	}
}

func writeJSONResponse(w io.Writer, payload jsonResponse) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}

func validateProviderRouteOverride(cfg *config.Config, route string) error {
	route = strings.TrimSpace(route)
	if route == "" {
		return nil
	}
	available := availableProviderRoutes(cfg)
	if len(available) == 0 {
		return fmt.Errorf("manual-trigger: --llm-route requires configured llm routes")
	}
	for _, candidate := range available {
		if candidate == route {
			return nil
		}
	}
	return fmt.Errorf("manual-trigger: unknown --llm-route %q (available: %s)", route, strings.Join(available, ", "))
}

func availableProviderRoutes(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	if len(cfg.LLM.Routes) > 0 {
		routes := make([]string, 0, len(cfg.LLM.Routes))
		for route := range cfg.LLM.Routes {
			trimmed := strings.TrimSpace(route)
			if trimmed == "" {
				continue
			}
			routes = append(routes, trimmed)
		}
		return routes
	}
	if strings.TrimSpace(cfg.AnthropicBaseURL) == "" && strings.TrimSpace(cfg.AnthropicModel) == "" && strings.TrimSpace(cfg.AnthropicAPIKey) == "" {
		return nil
	}
	return []string{"default", "secondary"}
}

func newDefaultService(cfg *config.Config, sqlDB *sql.DB, pollInterval time.Duration) manualTriggerService {
	logger := logging.NewLogger(slog.LevelInfo)
	client, err := gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
	if err != nil {
		return failingService{err: fmt.Errorf("configure gitlab client: %w", err)}
	}
	return manualtrigger.NewService(logger, sqlDB, client, cfg.GitLabBaseURL, manualtrigger.WithPollInterval(pollInterval))
}

type failingService struct {
	err error
}

func (f failingService) Trigger(context.Context, manualtrigger.TriggerInput) (manualtrigger.TriggerResult, error) {
	return manualtrigger.TriggerResult{}, f.err
}

func (f failingService) WaitForTerminalRun(context.Context, int64) (db.ReviewRun, error) {
	return db.ReviewRun{}, f.err
}
