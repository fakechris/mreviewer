package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		env       map[string]string
		wantPort  string
		wantDSN   string
		wantEnv   string
		wantModel string
	}{
		{
			name: "yaml only",
			yaml: `port: "3200"
mysql_dsn: "yaml-dsn"
app_env: "staging"
anthropic_model: "yaml-model"`,
			env:       nil,
			wantPort:  "3200",
			wantDSN:   "yaml-dsn",
			wantEnv:   "staging",
			wantModel: "yaml-model",
		},
		{
			name: "env overrides yaml",
			yaml: `port: "3200"
mysql_dsn: "yaml-dsn"
app_env: "staging"
anthropic_model: "yaml-model"`,
			env: map[string]string{
				"PORT":            "3100",
				"MYSQL_DSN":       "env-dsn",
				"APP_ENV":         "production",
				"ANTHROPIC_MODEL": "env-model",
			},
			wantPort:  "3100",
			wantDSN:   "env-dsn",
			wantEnv:   "production",
			wantModel: "env-model",
		},
		{
			name:      "env only no yaml",
			yaml:      "",
			env:       map[string]string{"PORT": "3150", "MYSQL_DSN": "env-only-dsn"},
			wantPort:  "3150",
			wantDSN:   "env-only-dsn",
			wantEnv:   "development", // default
			wantModel: "",
		},
		{
			name:      "defaults when nothing set",
			yaml:      "",
			env:       nil,
			wantPort:  "3100",
			wantDSN:   "",
			wantEnv:   "development",
			wantModel: "",
		},
		{
			name: "partial env override",
			yaml: `port: "3200"
mysql_dsn: "yaml-dsn"
app_env: "staging"`,
			env:       map[string]string{"MYSQL_DSN": "env-dsn"},
			wantPort:  "3200",
			wantDSN:   "env-dsn",
			wantEnv:   "staging",
			wantModel: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Clean env vars before each test case.
			for _, m := range envMapping {
				t.Setenv(m.envVar, "")
				os.Unsetenv(m.envVar)
			}

			// Set test env vars.
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			// Write YAML to temp file if provided.
			yamlPath := ""
			if tc.yaml != "" {
				dir := t.TempDir()
				yamlPath = filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(yamlPath, []byte(tc.yaml), 0644); err != nil {
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
			if cfg.AnthropicModel != tc.wantModel {
				t.Errorf("AnthropicModel = %q, want %q", cfg.AnthropicModel, tc.wantModel)
			}
		})
	}
}

func TestConfigMissingYAML(t *testing.T) {
	// Loading a non-existent YAML path should not error.
	cfg, err := Load("/tmp/does-not-exist-config-xyz.yaml")
	if err != nil {
		t.Fatalf("Load() with missing file should not error: %v", err)
	}
	if cfg.Port != "3100" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "3100")
	}
}

func TestConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(yamlPath, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatalf("writing bad yaml: %v", err)
	}
	_, err := Load(yamlPath)
	if err == nil {
		t.Fatal("Load() with invalid YAML should return error")
	}
}

func TestConfigAllEnvVars(t *testing.T) {
	// Verify every env var mapping works.
	envVals := map[string]string{
		"APP_ENV":               "test",
		"PORT":                  "9999",
		"MYSQL_DSN":             "test-dsn",
		"REDIS_ADDR":            "test-redis",
		"GITLAB_BASE_URL":       "https://test-gitlab",
		"GITLAB_TOKEN":          "test-token",
		"GITLAB_WEBHOOK_SECRET": "test-secret",
		"ANTHROPIC_BASE_URL":    "https://test-anthropic",
		"ANTHROPIC_API_KEY":     "test-key",
		"ANTHROPIC_MODEL":       "test-model",
	}

	for k, v := range envVals {
		t.Setenv(k, v)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AppEnv != "test" {
		t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, "test")
	}
	if cfg.Port != "9999" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9999")
	}
	if cfg.MySQLDSN != "test-dsn" {
		t.Errorf("MySQLDSN = %q, want %q", cfg.MySQLDSN, "test-dsn")
	}
	if cfg.RedisAddr != "test-redis" {
		t.Errorf("RedisAddr = %q, want %q", cfg.RedisAddr, "test-redis")
	}
	if cfg.GitLabBaseURL != "https://test-gitlab" {
		t.Errorf("GitLabBaseURL = %q, want %q", cfg.GitLabBaseURL, "https://test-gitlab")
	}
	if cfg.GitLabToken != "test-token" {
		t.Errorf("GitLabToken = %q, want %q", cfg.GitLabToken, "test-token")
	}
	if cfg.GitLabWebhookSecret != "test-secret" {
		t.Errorf("GitLabWebhookSecret = %q, want %q", cfg.GitLabWebhookSecret, "test-secret")
	}
	if cfg.AnthropicBaseURL != "https://test-anthropic" {
		t.Errorf("AnthropicBaseURL = %q, want %q", cfg.AnthropicBaseURL, "https://test-anthropic")
	}
	if cfg.AnthropicAPIKey != "test-key" {
		t.Errorf("AnthropicAPIKey = %q, want %q", cfg.AnthropicAPIKey, "test-key")
	}
	if cfg.AnthropicModel != "test-model" {
		t.Errorf("AnthropicModel = %q, want %q", cfg.AnthropicModel, "test-model")
	}
}
