package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/logging"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	"github.com/mreviewer/mreviewer/internal/gitlab"
)

const (
	defaultPersonalConfigPath = "config.yaml"
	defaultPersonalPort       = "3100"
)

type initOptions struct {
	configPath string
	force      bool
	provider   string
	dryRun     bool
	verbose    int
}

type doctorOptions struct {
	configPath string
	jsonOutput bool
	verbose    int
}

type doctorReport struct {
	OK          bool          `json:"ok"`
	ConfigPath  string        `json:"config_path"`
	DatabaseDSN string        `json:"database_dsn"`
	Dialect     string        `json:"dialect,omitempty"`
	Checks      []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func runInitCommand(args []string, stdout io.Writer, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	opts, err := parseInitOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cliTracef(stderr, opts.verbose, 2, "cli: rendering init config (provider=%s path=%s dry-run=%t)", opts.provider, opts.configPath, opts.dryRun)
	content, err := renderPersonalConfig(opts.provider)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init failed: %v\n", err)
		return 1
	}
	if opts.dryRun {
		_, _ = fmt.Fprintln(stdout, "# dry-run: config was not written")
		_, _ = fmt.Fprint(stdout, content)
		return 0
	}
	if err := writePersonalConfig(opts.configPath, content, opts.force); err != nil {
		_, _ = fmt.Fprintf(stderr, "init failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", opts.configPath)
	_, _ = fmt.Fprintln(stdout, "next:")
	_, _ = fmt.Fprintf(stdout, "  1. export provider and platform tokens for the generated template\n")
	_, _ = fmt.Fprintf(stdout, "  2. run `mreviewer doctor --config %s`\n", opts.configPath)
	_, _ = fmt.Fprintf(stdout, "  3. run `mreviewer review --target <github-or-gitlab-pr-url>` or `mreviewer serve --config %s`\n", opts.configPath)
	return 0
}

func parseInitOptions(args []string, stderr io.Writer) (initOptions, error) {
	cleanedArgs, commonFlags, err := extractCommonCLIFlags(args)
	if err != nil {
		return initOptions{}, err
	}
	fs := flag.NewFlagSet("mreviewer init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := initOptions{
		configPath: defaultPersonalConfigPath,
		provider:   "openai",
		verbose:    commonFlags.verbose,
	}
	setFlagSetUsage(fs, `
Usage: mreviewer init [options]

Generate a personal config template for local CLI usage.

Agent-friendly flags: --dry-run (alias: --dryrun), --verbose, -vv, -vvv, -vvvv

Examples:
  mreviewer init
  mreviewer init --provider minimax
  mreviewer init --config ~/.config/mreviewer/config.yaml --dry-run
`)
	fs.StringVar(&opts.configPath, "config", defaultPersonalConfigPath, "Path to config file")
	fs.BoolVar(&opts.force, "force", false, "Overwrite config file if it already exists")
	fs.StringVar(&opts.provider, "provider", "openai", "Provider template to initialize: openai|minimax|anthropic")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "Render the config template without writing files")
	fs.BoolVar(&opts.dryRun, "dryrun", false, "Alias for --dry-run")
	fs.Bool("verbose", false, "Increase detail; repeat -vv/-vvv/-vvvv for debug traces")
	if err := fs.Parse(cleanedArgs); err != nil {
		return initOptions{}, err
	}
	opts.provider = strings.ToLower(strings.TrimSpace(opts.provider))
	return opts, nil
}

func renderPersonalConfig(provider string) (string, error) {
	type providerTemplate struct {
		ModelID   string
		ChainID   string
		Provider  string
		BaseURL   string
		APIKeyEnv string
		Model     string
		Output    string
		Tokens    string
		Reasoning string
	}
	templateByProvider := map[string]providerTemplate{
		"openai": {
			ModelID:   "openai_default",
			ChainID:   "review_primary",
			Provider:  "openai",
			BaseURL:   "https://api.openai.com/v1",
			APIKeyEnv: "${OPENAI_API_KEY}",
			Model:     "gpt-5.4",
			Output:    "json_schema",
			Tokens:    "max_completion_tokens: 12000",
			Reasoning: "reasoning_effort: medium",
		},
		"minimax": {
			ModelID:   "minimax_default",
			ChainID:   "review_primary",
			Provider:  "minimax",
			BaseURL:   "https://api.minimaxi.com/anthropic",
			APIKeyEnv: "${MINIMAX_API_KEY}",
			Model:     "MiniMax-M2.7-highspeed",
			Output:    "tool_call",
			Tokens:    "max_tokens: 4096",
		},
		"anthropic": {
			ModelID:   "anthropic_default",
			ChainID:   "review_primary",
			Provider:  "anthropic",
			BaseURL:   "https://api.anthropic.com",
			APIKeyEnv: "${ANTHROPIC_API_KEY}",
			Model:     "claude-sonnet-4-6",
			Output:    "tool_call",
			Tokens:    "max_tokens: 12000",
		},
	}
	chosen, ok := templateByProvider[provider]
	if !ok {
		return "", fmt.Errorf("unsupported provider template %q", provider)
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf(`# Personal CLI config generated by mreviewer init.
# Fill in env vars before running mreviewer doctor/review/serve.

app_env: development
port: "%s"
database_dsn: "file:.mreviewer/state/mreviewer.db?_pragma=busy_timeout(5000)"

admin_token: ${MREVIEWER_ADMIN_TOKEN}

gitlab_base_url: ${GITLAB_BASE_URL}
gitlab_token: ${GITLAB_TOKEN}
gitlab_webhook_secret: ${GITLAB_WEBHOOK_SECRET}

github_base_url: ${GITHUB_BASE_URL}
github_token: ${GITHUB_TOKEN}
github_webhook_secret: ${GITHUB_WEBHOOK_SECRET}

models:
  %s:
    provider: %s
    base_url: %s
    api_key: %s
    model: %s
    output_mode: %s
`, defaultPersonalPort, chosen.ModelID, chosen.Provider, chosen.BaseURL, chosen.APIKeyEnv, chosen.Model, chosen.Output))
	if trimmed := strings.TrimSpace(chosen.Tokens); trimmed != "" {
		builder.WriteString(fmt.Sprintf("    %s\n", trimmed))
	}
	if trimmed := strings.TrimSpace(chosen.Reasoning); trimmed != "" {
		builder.WriteString(fmt.Sprintf("    %s\n", trimmed))
	}
	builder.WriteString(fmt.Sprintf(`
model_chains:
  %s:
    primary: %s
    fallbacks: []

review:
  model_chain: %s
  packs:
    - security
    - architecture
    - database
`, chosen.ChainID, chosen.ModelID, chosen.ChainID))
	return builder.String(), nil
}

func writePersonalConfig(path, content string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is required")
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.MkdirAll(".mreviewer/state", 0o755); err != nil {
		return fmt.Errorf("create sqlite state directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func runDoctorCommand(args []string, stdout io.Writer, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	opts, err := parseDoctorOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cliTracef(stderr, opts.verbose, 2, "cli: running doctor with config %s", opts.configPath)
	report := doctorReport{ConfigPath: opts.configPath}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "config", Status: "fail", Message: err.Error()})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	report.Checks = append(report.Checks, doctorCheck{Name: "config", Status: "pass", Message: "configuration loaded"})

	applyPersonalDefaults(cfg)
	report.DatabaseDSN = cfg.DSN()
	if strings.TrimSpace(report.DatabaseDSN) == "" {
		report.Checks = append(report.Checks, doctorCheck{Name: "database", Status: "fail", Message: "database_dsn/mysql_dsn is empty"})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	if err := ensureSQLiteParentDir(report.DatabaseDSN); err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "database", Status: "fail", Message: err.Error()})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	dbConn, dialect, err := database.OpenWithDialect(report.DatabaseDSN)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "database", Status: "fail", Message: err.Error()})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	report.Dialect = dialect.String()
	_ = dbConn.Close()
	report.Checks = append(report.Checks, doctorCheck{Name: "database", Status: "pass", Message: fmt.Sprintf("%s connection opened", dialect)})

	defaultRoute, fallbackRoutes, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "llm", Status: "fail", Message: err.Error()})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	logger := logging.NewLogger(slog.LevelWarn)
	registry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, fallbackRoutes, providerConfigs)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "llm", Status: "fail", Message: err.Error()})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	report.Checks = append(report.Checks, doctorCheck{Name: "llm", Status: "pass", Message: fmt.Sprintf("default route %s with %d route(s)", defaultRoute, len(registry.Routes()))})

	platformPasses := 0
	if strings.TrimSpace(cfg.GitHubToken) != "" {
		if _, err := platformgithub.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken); err != nil {
			report.Checks = append(report.Checks, doctorCheck{Name: "github", Status: "fail", Message: err.Error()})
		} else {
			platformPasses++
			report.Checks = append(report.Checks, doctorCheck{Name: "github", Status: "pass", Message: "GitHub client configured"})
		}
	} else {
		report.Checks = append(report.Checks, doctorCheck{Name: "github", Status: "warn", Message: "GITHUB_TOKEN is not configured"})
	}
	if strings.TrimSpace(cfg.GitLabToken) != "" {
		if strings.TrimSpace(cfg.GitLabBaseURL) == "" {
			report.Checks = append(report.Checks, doctorCheck{Name: "gitlab", Status: "warn", Message: "GITLAB_TOKEN is configured but GITLAB_BASE_URL is empty; skipping GitLab client"})
		} else if _, err := gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken); err != nil {
			report.Checks = append(report.Checks, doctorCheck{Name: "gitlab", Status: "fail", Message: err.Error()})
		} else {
			platformPasses++
			report.Checks = append(report.Checks, doctorCheck{Name: "gitlab", Status: "pass", Message: "GitLab client configured"})
		}
	} else {
		report.Checks = append(report.Checks, doctorCheck{Name: "gitlab", Status: "warn", Message: "GITLAB_TOKEN is not configured"})
	}
	if platformPasses == 0 {
		report.Checks = append(report.Checks, doctorCheck{Name: "platforms", Status: "fail", Message: "configure at least one of GITHUB_TOKEN or GITLAB_TOKEN"})
		return writeDoctorReport(stdout, report, opts.jsonOutput, 1)
	}
	report.Checks = append(report.Checks, doctorCheck{Name: "platforms", Status: "pass", Message: fmt.Sprintf("%d platform client(s) configured", platformPasses)})
	report.OK = true
	return writeDoctorReport(stdout, report, opts.jsonOutput, 0)
}

func parseDoctorOptions(args []string, stderr io.Writer) (doctorOptions, error) {
	cleanedArgs, commonFlags, err := extractCommonCLIFlags(args)
	if err != nil {
		return doctorOptions{}, err
	}
	fs := flag.NewFlagSet("mreviewer doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagSetUsage(fs, `
Usage: mreviewer doctor [options]

Validate config, database, LLM routes, and platform credentials.

Agent-friendly flags: --json, --verbose, -vv, -vvv, -vvvv

Examples:
  mreviewer doctor
  mreviewer doctor --json
  mreviewer doctor --config ~/.config/mreviewer/config.yaml
`)
	opts := doctorOptions{configPath: defaultPersonalConfigPath, verbose: commonFlags.verbose}
	fs.StringVar(&opts.configPath, "config", defaultPersonalConfigPath, "Path to config file")
	fs.BoolVar(&opts.jsonOutput, "json", false, "Emit machine-readable doctor output")
	fs.Bool("verbose", false, "Increase detail; repeat -vv/-vvv/-vvvv for debug traces")
	if err := fs.Parse(cleanedArgs); err != nil {
		return doctorOptions{}, err
	}
	return opts, nil
}

func writeDoctorReport(stdout io.Writer, report doctorReport, jsonOutput bool, exitCode int) int {
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return exitCode
	}
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(stdout, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
	}
	if report.DatabaseDSN != "" {
		_, _ = fmt.Fprintf(stdout, "database: %s\n", report.DatabaseDSN)
	}
	return exitCode
}
