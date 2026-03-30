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
			for _, envVar := range []string{"MINIMAX_API_KEY", "MINIMAX_BASE_URL", "MINIMAX_MODEL"} {
				t.Setenv(envVar, "")
				os.Unsetenv(envVar)
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
	for _, envVar := range []string{"MINIMAX_API_KEY", "MINIMAX_BASE_URL", "MINIMAX_MODEL"} {
		t.Setenv(envVar, "")
	}
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
	for _, envVar := range []string{"MINIMAX_API_KEY", "MINIMAX_BASE_URL", "MINIMAX_MODEL"} {
		t.Setenv(envVar, "")
	}
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

func TestConfigMiniMaxEnvFallback(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "minimax-secret")
	t.Setenv("MINIMAX_BASE_URL", "https://api.minimaxi.com/anthropic")
	t.Setenv("MINIMAX_MODEL", "MiniMax-M2.7-highspeed")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_MODEL", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AnthropicAPIKey != "minimax-secret" {
		t.Errorf("AnthropicAPIKey = %q, want minimax-secret", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicBaseURL != "https://api.minimaxi.com/anthropic" {
		t.Errorf("AnthropicBaseURL = %q, want MiniMax fallback URL", cfg.AnthropicBaseURL)
	}
	if cfg.AnthropicModel != "MiniMax-M2.7-highspeed" {
		t.Errorf("AnthropicModel = %q, want MiniMax fallback model", cfg.AnthropicModel)
	}
}

func TestConfigMiniMaxEnvOverridesYAMLDefaultsWhenAnthropicEnvUnset(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `anthropic_base_url: "https://yaml-anthropic"
anthropic_api_key: ""
anthropic_model: "MiniMax-M2.5"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, envVar := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		if err := os.Unsetenv(envVar); err != nil {
			t.Fatalf("Unsetenv(%s): %v", envVar, err)
		}
	}
	t.Setenv("MINIMAX_API_KEY", "minimax-secret")
	t.Setenv("MINIMAX_BASE_URL", "https://api.minimaxi.com/anthropic")
	t.Setenv("MINIMAX_MODEL", "MiniMax-M2.7-highspeed")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AnthropicBaseURL != "https://api.minimaxi.com/anthropic" {
		t.Errorf("AnthropicBaseURL = %q, want MiniMax env override", cfg.AnthropicBaseURL)
	}
	if cfg.AnthropicAPIKey != "minimax-secret" {
		t.Errorf("AnthropicAPIKey = %q, want minimax-secret", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicModel != "MiniMax-M2.7-highspeed" {
		t.Errorf("AnthropicModel = %q, want MiniMax env override model", cfg.AnthropicModel)
	}
}

func TestConfigParsesLLMRoutesFromYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `llm:
  default_route: minimax
  fallback_route: openai
  routes:
    minimax:
      provider: minimax
      base_url: https://api.minimaxi.com/anthropic
      api_key: minimax-key
      model: MiniMax-M2.7
      output_mode: tool_call
      temperature: 0.2
    openai:
      provider: openai
      base_url: https://api.openai.com/v1
      api_key: openai-key
      model: gpt-5.4
      output_mode: json_schema
      max_completion_tokens: 12000
      reasoning_effort: medium
      temperature: 0.2
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.LLM.DefaultRoute != "minimax" {
		t.Fatalf("LLM.DefaultRoute = %q, want minimax", cfg.LLM.DefaultRoute)
	}
	if cfg.LLM.FallbackRoute != "openai" {
		t.Fatalf("LLM.FallbackRoute = %q, want openai", cfg.LLM.FallbackRoute)
	}
	if len(cfg.LLM.Routes) != 2 {
		t.Fatalf("LLM.Routes = %d, want 2", len(cfg.LLM.Routes))
	}
	if cfg.LLM.Routes["minimax"].Provider != "minimax" {
		t.Fatalf("minimax provider = %q, want minimax", cfg.LLM.Routes["minimax"].Provider)
	}
	if cfg.LLM.Routes["openai"].Provider != "openai" {
		t.Fatalf("openai provider = %q, want openai", cfg.LLM.Routes["openai"].Provider)
	}
	if cfg.LLM.Routes["openai"].OutputMode != "json_schema" {
		t.Fatalf("openai output_mode = %q, want json_schema", cfg.LLM.Routes["openai"].OutputMode)
	}
	if cfg.LLM.Routes["openai"].MaxCompletionTokens != 12000 {
		t.Fatalf("openai max_completion_tokens = %d, want 12000", cfg.LLM.Routes["openai"].MaxCompletionTokens)
	}
	if cfg.LLM.Routes["openai"].ReasoningEffort != "medium" {
		t.Fatalf("openai reasoning_effort = %q, want medium", cfg.LLM.Routes["openai"].ReasoningEffort)
	}
}

func TestConfigExpandsEnvVarsInsideYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	t.Setenv("OPENAI_API_KEY", "openai-from-env")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")

	content := `llm:
  default_route: openai
  fallback_route: openai
  routes:
    openai:
      provider: openai
      base_url: ${OPENAI_BASE_URL}
      api_key: ${OPENAI_API_KEY}
      model: gpt-5.4
      output_mode: json_schema
      max_completion_tokens: 12000
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	route := cfg.LLM.Routes["openai"]
	if route.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("route.BaseURL = %q, want env-expanded base URL", route.BaseURL)
	}
	if route.APIKey != "openai-from-env" {
		t.Fatalf("route.APIKey = %q, want env-expanded api key", route.APIKey)
	}
}

func TestConfigPreservesLiteralDollarSignsInsideYAML(t *testing.T) {
	for _, m := range envMapping {
		t.Setenv(m.envVar, "")
		os.Unsetenv(m.envVar)
	}
	for _, envVar := range []string{"MINIMAX_API_KEY", "MINIMAX_BASE_URL", "MINIMAX_MODEL"} {
		t.Setenv(envVar, "")
		os.Unsetenv(envVar)
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `mysql_dsn: "user:pa$$word@tcp(localhost:3306)/mreviewer"
anthropic_api_key: "sk$ecret"
llm:
  default_route: literal
  fallback_route: literal
  routes:
    literal:
      provider: openai
      base_url: https://api.openai.com/v1
      api_key: "route$token"
      model: gpt-5.4
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
	if cfg.AnthropicAPIKey != "sk$ecret" {
		t.Fatalf("AnthropicAPIKey = %q, want literal dollar sign preserved", cfg.AnthropicAPIKey)
	}
	if got := cfg.LLM.Routes["literal"].APIKey; got != "route$token" {
		t.Fatalf("route APIKey = %q, want literal dollar sign preserved", got)
	}
}

func TestConfigEnvOverridesLLMRoutePointers(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `llm:
  default_route: minimax
  fallback_route: openai
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	t.Setenv("LLM_DEFAULT_ROUTE", "anthropic")
	t.Setenv("LLM_FALLBACK_ROUTE", "minimax")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.LLM.DefaultRoute != "anthropic" {
		t.Fatalf("LLM.DefaultRoute = %q, want anthropic", cfg.LLM.DefaultRoute)
	}
	if cfg.LLM.FallbackRoute != "minimax" {
		t.Fatalf("LLM.FallbackRoute = %q, want minimax", cfg.LLM.FallbackRoute)
	}
}
