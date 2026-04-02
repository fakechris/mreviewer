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
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
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
						OutputMode:          "json_schema",
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
				OutputMode:          "json_schema",
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

type serverStarterFunc func(context.Context) error

func (f serverStarterFunc) Start(ctx context.Context) error {
	return f(ctx)
}
