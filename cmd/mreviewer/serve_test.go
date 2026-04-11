package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
)

func TestRunServeWithDepsAppliesDefaultSQLiteDSN(t *testing.T) {
	var migratedDSN string
	var openedDSN string
	var workerConfig *config.Config

	exitCode := runServeWithDeps([]string{"--port", "3200"}, serveDeps{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				GitHubToken: "github-token",
				Models: map[string]config.ModelConfig{
					"openai_default": {
						Provider:            "openai",
						BaseURL:             "https://api.openai.com/v1",
						APIKey:              "test-key",
						Model:               "gpt-5.4",
						OutputMode:          "tool_call",
						MaxCompletionTokens: 12000,
						ReasoningEffort:     "medium",
					},
				},
				ModelChains: map[string]config.ModelChainConfig{
					"review_primary": {Primary: "openai_default"},
				},
				Review: config.ReviewConfig{ModelChain: "review_primary"},
			}, nil
		},
		migrateUpFromDSN: func(dsn string) error {
			migratedDSN = dsn
			return nil
		},
		openWithDialect: func(dsn string) (*sql.DB, database.Dialect, error) {
			openedDSN = dsn
			db, _, err := database.OpenWithDialect("file::memory:?cache=shared")
			return db, database.DialectSQLite, err
		},
		newIngressMux: func(_ *slog.Logger, cfg *config.Config, _ *sql.DB, _ database.Dialect) (http.Handler, error) {
			if cfg.Port != "3200" {
				t.Fatalf("cfg.Port = %q, want 3200", cfg.Port)
			}
			return http.NewServeMux(), nil
		},
		newWorker: func(_ *slog.Logger, cfg *config.Config, _ *sql.DB, _ database.Dialect) (*personalWorkerRuntime, error) {
			workerConfig = cfg
			return &personalWorkerRuntime{}, nil
		},
		newServer: func(string, http.Handler, *slog.Logger) serverStarter {
			return serverStarterFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			})
		},
		stdout: io.Discard,
		stderr: &bytes.Buffer{},
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if migratedDSN != defaultPersonalSQLiteDSN() {
		t.Fatalf("migratedDSN = %q, want %q", migratedDSN, defaultPersonalSQLiteDSN())
	}
	if openedDSN != defaultPersonalSQLiteDSN() {
		t.Fatalf("openedDSN = %q, want %q", openedDSN, defaultPersonalSQLiteDSN())
	}
	if workerConfig == nil || workerConfig.Port != "3200" {
		t.Fatalf("workerConfig.Port = %q, want 3200", workerConfig.Port)
	}
}

func TestApplyPersonalDefaultsSetsSQLiteAndGitHubBaseURL(t *testing.T) {
	cfg := &config.Config{}
	applyPersonalDefaults(cfg)
	if cfg.Port != defaultPersonalPort {
		t.Fatalf("cfg.Port = %q, want %q", cfg.Port, defaultPersonalPort)
	}
	if cfg.DatabaseDSN != defaultPersonalSQLiteDSN() {
		t.Fatalf("cfg.DatabaseDSN = %q, want %q", cfg.DatabaseDSN, defaultPersonalSQLiteDSN())
	}
	if cfg.GitHubBaseURL != "https://api.github.com" {
		t.Fatalf("cfg.GitHubBaseURL = %q, want https://api.github.com", cfg.GitHubBaseURL)
	}
}

func TestRunServeWithDepsAllowsIncompleteGitLabWhenGitHubIsConfigured(t *testing.T) {
	cfg := &config.Config{
		GitHubToken: "github-token",
		GitLabToken: "gitlab-token",
		Models: map[string]config.ModelConfig{
			"openai_default": {
				Provider:            "openai",
				BaseURL:             "https://api.openai.com/v1",
				APIKey:              "test-key",
				Model:               "gpt-5.4",
				OutputMode:          "tool_call",
				MaxCompletionTokens: 12000,
				ReasoningEffort:     "medium",
			},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {Primary: "openai_default"},
		},
		Review: config.ReviewConfig{ModelChain: "review_primary"},
	}
	if err := validateServeConfig(cfg); err != nil {
		t.Fatalf("validateServeConfig() error = %v, want nil", err)
	}

	exitCode := runServeWithDeps([]string{"--port", "3200"}, serveDeps{
		loadConfig: func(string) (*config.Config, error) {
			return cfg, nil
		},
		migrateUpFromDSN: func(string) error { return nil },
		openWithDialect: func(string) (*sql.DB, database.Dialect, error) {
			db, _, err := database.OpenWithDialect("file::memory:?cache=shared")
			return db, database.DialectSQLite, err
		},
		newIngressMux: func(_ *slog.Logger, _ *config.Config, _ *sql.DB, _ database.Dialect) (http.Handler, error) {
			return http.NewServeMux(), nil
		},
		newWorker: func(_ *slog.Logger, _ *config.Config, _ *sql.DB, _ database.Dialect) (*personalWorkerRuntime, error) {
			return &personalWorkerRuntime{}, nil
		},
		newServer: func(string, http.Handler, *slog.Logger) serverStarter {
			return serverStarterFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			})
		},
		stdout: io.Discard,
		stderr: &bytes.Buffer{},
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
}

func TestRunServeWithDepsDryRunSkipsMigrateAndStart(t *testing.T) {
	var migrated bool
	var opened bool
	var serverStarted bool
	var workerBuilt bool
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runServeWithDeps([]string{"--port", "3200", "--dry-run", "-vv"}, serveDeps{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				GitHubToken: "github-token",
				Models: map[string]config.ModelConfig{
					"openai_default": {
						Provider:   "openai",
						BaseURL:    "https://api.openai.com/v1",
						APIKey:     "test-key",
						Model:      "gpt-5.4",
						OutputMode: "tool_call",
					},
				},
				ModelChains: map[string]config.ModelChainConfig{
					"review_primary": {Primary: "openai_default"},
				},
				Review: config.ReviewConfig{ModelChain: "review_primary"},
			}, nil
		},
		migrateUpFromDSN: func(string) error {
			migrated = true
			return nil
		},
		openWithDialect: func(string) (*sql.DB, database.Dialect, error) {
			opened = true
			db, _, err := database.OpenWithDialect("file::memory:?cache=shared")
			return db, database.DialectSQLite, err
		},
		newIngressMux: func(_ *slog.Logger, _ *config.Config, _ *sql.DB, _ database.Dialect) (http.Handler, error) {
			return http.NewServeMux(), nil
		},
		newWorker: func(_ *slog.Logger, _ *config.Config, _ *sql.DB, _ database.Dialect) (*personalWorkerRuntime, error) {
			workerBuilt = true
			return &personalWorkerRuntime{}, nil
		},
		newServer: func(string, http.Handler, *slog.Logger) serverStarter {
			serverStarted = true
			return serverStarterFunc(func(context.Context) error { return nil })
		},
		stdout: &stdout,
		stderr: &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	if migrated {
		t.Fatal("migrateUpFromDSN called during dry-run")
	}
	if opened {
		t.Fatal("openWithDialect called during dry-run")
	}
	if workerBuilt {
		t.Fatal("newWorker called during dry-run")
	}
	if serverStarted {
		t.Fatal("server started during dry-run")
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout missing dry-run marker: %q", stdout.String())
	}
}

func TestValidateServeConfigExplainsIncompleteGitLabWithoutGitHub(t *testing.T) {
	cfg := &config.Config{
		GitLabToken: "gitlab-token",
		Models: map[string]config.ModelConfig{
			"openai_default": {
				Provider:            "openai",
				BaseURL:             "https://api.openai.com/v1",
				APIKey:              "test-key",
				Model:               "gpt-5.4",
				OutputMode:          "tool_call",
				MaxCompletionTokens: 12000,
			},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {Primary: "openai_default"},
		},
		Review: config.ReviewConfig{ModelChain: "review_primary"},
	}

	err := validateServeConfig(cfg)
	if err == nil {
		t.Fatal("validateServeConfig() error = nil, want error")
	}
	if err.Error() != "configure at least one platform: set GITHUB_TOKEN, or both GITLAB_TOKEN and GITLAB_BASE_URL" {
		t.Fatalf("validateServeConfig() error = %q", err.Error())
	}
}

func TestEnsureSQLiteParentDirCreatesParentDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := "file:" + filepath.Join(tmpDir, "subdir", "mreviewer.db") + "?_pragma=busy_timeout(5000)"
	if err := ensureSQLiteParentDir(dsn); err != nil {
		t.Fatalf("ensureSQLiteParentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "subdir")); err != nil {
		t.Fatalf("expected sqlite parent dir: %v", err)
	}
}

func TestBuildServeReviewInputRequiresConfiguredPlatformClient(t *testing.T) {
	builder := reviewinput.NewBuilder(nil, nil, nil)

	_, err := buildServeReviewInput(context.Background(), core.ReviewTarget{Platform: core.PlatformGitHub}, nil, nil, builder)
	if err == nil || !strings.Contains(err.Error(), "github client is required") {
		t.Fatalf("github error = %v, want github client is required", err)
	}

	_, err = buildServeReviewInput(context.Background(), core.ReviewTarget{Platform: core.PlatformGitLab}, nil, nil, builder)
	if err == nil || !strings.Contains(err.Error(), "gitlab client is required") {
		t.Fatalf("gitlab error = %v, want gitlab client is required", err)
	}
}

type serverStarterFunc func(context.Context) error

func (f serverStarterFunc) Start(ctx context.Context) error {
	return f(ctx)
}
