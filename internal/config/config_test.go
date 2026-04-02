package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		env      map[string]string
		wantPort string
		wantDSN  string
		wantEnv  string
	}{
		{
			name: "yaml only",
			yaml: `port: "3200"
mysql_dsn: "yaml-dsn"
app_env: "staging"`,
			wantPort: "3200",
			wantDSN:  "yaml-dsn",
			wantEnv:  "staging",
		},
		{
			name: "env overrides yaml",
			yaml: `port: "3200"
mysql_dsn: "yaml-dsn"
app_env: "staging"`,
			env: map[string]string{
				"PORT":      "3100",
				"MYSQL_DSN": "env-dsn",
				"APP_ENV":   "production",
			},
			wantPort: "3100",
			wantDSN:  "env-dsn",
			wantEnv:  "production",
		},
		{
			name:     "env only no yaml",
			env:      map[string]string{"PORT": "3150", "MYSQL_DSN": "env-only-dsn"},
			wantPort: "3150",
			wantDSN:  "env-only-dsn",
			wantEnv:  "development",
		},
		{
			name:     "defaults when nothing set",
			wantPort: "3100",
			wantDSN:  "",
			wantEnv:  "development",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			yamlPath := ""
			if tc.yaml != "" {
				dir := t.TempDir()
				yamlPath = filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(yamlPath, []byte(tc.yaml), 0o644); err != nil {
					t.Fatalf("writing yaml: %v", err)
				}
			}

			cfg, err := Load(yamlPath)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}

			if cfg.Port != tc.wantPort {
				t.Errorf("Port = %q, want %q", cfg.Port, tc.wantPort)
			}
			if cfg.MySQLDSN != tc.wantDSN {
				t.Errorf("MySQLDSN = %q, want %q", cfg.MySQLDSN, tc.wantDSN)
			}
			if cfg.AppEnv != tc.wantEnv {
				t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, tc.wantEnv)
			}
		})
	}
}

func TestConfigMissingYAML(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load("/tmp/does-not-exist-config-xyz.yaml")
	if err != nil {
		t.Fatalf("Load() with missing file should not error: %v", err)
	}
	if cfg.Port != "3100" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "3100")
	}
}

func TestConfigInvalidYAML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(yamlPath, []byte("{{invalid yaml"), 0o644); err != nil {
		t.Fatalf("writing bad yaml: %v", err)
	}
	if _, err := Load(yamlPath); err == nil {
		t.Fatal("Load() with invalid YAML should return error")
	}
}

func TestConfigAllEnvVars(t *testing.T) {
	clearConfigEnv(t)
	envVals := map[string]string{
		"APP_ENV":                 "test",
		"MREVIEWER_ADMIN_TOKEN":   "admin-secret",
		"PORT":                    "9999",
		"MYSQL_DSN":               "test-dsn",
		"REDIS_ADDR":              "test-redis",
		"GITLAB_BASE_URL":         "https://test-gitlab",
		"GITLAB_TOKEN":            "test-token",
		"GITLAB_WEBHOOK_SECRET":   "test-secret",
		"GITHUB_BASE_URL":         "https://api.github.com",
		"GITHUB_TOKEN":            "github-token",
		"GITHUB_WEBHOOK_SECRET":   "github-secret",
		"REVIEW_MODEL_CHAIN":      "review_primary",
		"REVIEW_ADVISOR_CHAIN":    "advisor_chain",
		"REVIEW_PACKS":            "security,architecture,database",
		"REVIEW_COMPARE_REVIEWERS": "github:gemini,gitlab:coderabbit",
	}
	for k, v := range envVals {
		t.Setenv(k, v)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AppEnv != "test" {
		t.Errorf("AppEnv = %q, want test", cfg.AppEnv)
	}
	if cfg.AdminToken != "admin-secret" {
		t.Errorf("AdminToken = %q, want admin-secret", cfg.AdminToken)
	}
	if cfg.Port != "9999" {
		t.Errorf("Port = %q, want 9999", cfg.Port)
	}
	if cfg.MySQLDSN != "test-dsn" {
		t.Errorf("MySQLDSN = %q, want test-dsn", cfg.MySQLDSN)
	}
	if cfg.RedisAddr != "test-redis" {
		t.Errorf("RedisAddr = %q, want test-redis", cfg.RedisAddr)
	}
	if cfg.GitLabBaseURL != "https://test-gitlab" {
		t.Errorf("GitLabBaseURL = %q, want https://test-gitlab", cfg.GitLabBaseURL)
	}
	if cfg.GitHubBaseURL != "https://api.github.com" {
		t.Errorf("GitHubBaseURL = %q, want https://api.github.com", cfg.GitHubBaseURL)
	}
	if cfg.Review.ModelChain != "review_primary" {
		t.Errorf("Review.ModelChain = %q, want review_primary", cfg.Review.ModelChain)
	}
	if cfg.Review.AdvisorChain != "advisor_chain" {
		t.Errorf("Review.AdvisorChain = %q, want advisor_chain", cfg.Review.AdvisorChain)
	}
	if got, want := cfg.Review.Packs, []string{"security", "architecture", "database"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Review.Packs = %#v, want %#v", got, want)
	}
	if got, want := cfg.Review.CompareReviewers, []string{"github:gemini", "gitlab:coderabbit"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Review.CompareReviewers = %#v, want %#v", got, want)
	}
}

func TestConfigExpandsEnvVarsInsideYAML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	t.Setenv("OPENAI_API_KEY", "openai-from-env")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")

	content := `models:
  openai_default:
    provider: openai
    base_url: ${OPENAI_BASE_URL}
    api_key: ${OPENAI_API_KEY}
    model: gpt-5.4
    output_mode: json_schema
model_chains:
  review_primary:
    primary: openai_default
review:
  model_chain: review_primary
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	model := cfg.Models["openai_default"]
	if model.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("model.BaseURL = %q, want env-expanded base URL", model.BaseURL)
	}
	if model.APIKey != "openai-from-env" {
		t.Fatalf("model.APIKey = %q, want env-expanded api key", model.APIKey)
	}
}

func TestConfigPreservesLiteralDollarSignsInsideYAML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `mysql_dsn: "user:pa$$word@tcp(localhost:3306)/mreviewer"
models:
  literal:
    provider: openai
    base_url: https://api.openai.com/v1
    api_key: "route$token"
    model: gpt-5.4
model_chains:
  review_primary:
    primary: literal
review:
  model_chain: review_primary
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.MySQLDSN != "user:pa$$word@tcp(localhost:3306)/mreviewer" {
		t.Fatalf("MySQLDSN = %q, want literal dollar signs preserved", cfg.MySQLDSN)
	}
	if got := cfg.Models["literal"].APIKey; got != "route$token" {
		t.Fatalf("model APIKey = %q, want literal dollar sign preserved", got)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, m := range envMapping {
		t.Setenv(m.envVar, "")
		_ = os.Unsetenv(m.envVar)
	}
}
