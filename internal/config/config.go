// Package config loads application configuration from environment variables
// and a YAML file. Environment variables always take precedence over YAML values.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var braceEnvPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config holds all application configuration values.
type Config struct {
	AppEnv string `yaml:"app_env"`
	Port   string `yaml:"port"`

	AdminToken string `yaml:"admin_token"`

	MySQLDSN    string `yaml:"mysql_dsn"`
	DatabaseDSN string `yaml:"database_dsn"`
	RedisAddr   string `yaml:"redis_addr"`

	GitLabBaseURL       string `yaml:"gitlab_base_url"`
	GitLabToken         string `yaml:"gitlab_token"`
	GitLabWebhookSecret string `yaml:"gitlab_webhook_secret"`
	GitHubBaseURL       string `yaml:"github_base_url"`
	GitHubToken         string `yaml:"github_token"`
	GitHubWebhookSecret string `yaml:"github_webhook_secret"`

	Models      map[string]ModelConfig      `yaml:"models"`
	ModelChains map[string]ModelChainConfig `yaml:"model_chains"`
	Review      ReviewConfig                `yaml:"review"`
}

type ModelConfig struct {
	Provider            string  `yaml:"provider"`
	BaseURL             string  `yaml:"base_url"`
	APIKey              string  `yaml:"api_key"`
	Model               string  `yaml:"model"`
	OutputMode          string  `yaml:"output_mode"`
	Temperature         float64 `yaml:"temperature"`
	MaxTokens           int64   `yaml:"max_tokens"`
	MaxCompletionTokens int64   `yaml:"max_completion_tokens"`
	ReasoningEffort     string  `yaml:"reasoning_effort"`
}

type ModelChainConfig struct {
	Primary   string   `yaml:"primary"`
	Fallbacks []string `yaml:"fallbacks"`
}

type ReviewConfig struct {
	ModelChain       string   `yaml:"model_chain"`
	AdvisorChain     string   `yaml:"advisor_chain"`
	Packs            []string `yaml:"packs"`
	CompareReviewers []string `yaml:"compare_reviewers"`
}

// envMapping maps Config field setters to their environment variable names.
// Each entry is a function that sets the corresponding Config field.
var envMapping = []struct {
	envVar string
	setter func(*Config, string)
}{
	{"APP_ENV", func(c *Config, v string) { c.AppEnv = v }},
	{"PORT", func(c *Config, v string) { c.Port = v }},
	{"ADMIN_TOKEN", func(c *Config, v string) { c.AdminToken = v }},
	{"MREVIEWER_ADMIN_TOKEN", func(c *Config, v string) { c.AdminToken = v }},
	{"MYSQL_DSN", func(c *Config, v string) { c.MySQLDSN = v }},
	{"DATABASE_DSN", func(c *Config, v string) { c.DatabaseDSN = v }},
	{"REDIS_ADDR", func(c *Config, v string) { c.RedisAddr = v }},
	{"GITLAB_BASE_URL", func(c *Config, v string) { c.GitLabBaseURL = v }},
	{"GITLAB_TOKEN", func(c *Config, v string) { c.GitLabToken = v }},
	{"GITLAB_WEBHOOK_SECRET", func(c *Config, v string) { c.GitLabWebhookSecret = v }},
	{"GITHUB_BASE_URL", func(c *Config, v string) { c.GitHubBaseURL = v }},
	{"GITHUB_TOKEN", func(c *Config, v string) { c.GitHubToken = v }},
	{"GITHUB_WEBHOOK_SECRET", func(c *Config, v string) { c.GitHubWebhookSecret = v }},
	{"REVIEW_MODEL_CHAIN", func(c *Config, v string) { c.Review.ModelChain = strings.TrimSpace(v) }},
	{"REVIEW_ADVISOR_CHAIN", func(c *Config, v string) { c.Review.AdvisorChain = strings.TrimSpace(v) }},
	{"REVIEW_PACKS", func(c *Config, v string) { c.Review.Packs = splitCSV(v) }},
	{"REVIEW_COMPARE_REVIEWERS", func(c *Config, v string) { c.Review.CompareReviewers = splitCSV(v) }},
}

// Load reads configuration first from the YAML file at yamlPath (if it exists
// and is readable), then overlays any set environment variables. Environment
// variables always win over YAML values.
func Load(yamlPath string) (*Config, error) {
	cfg := &Config{}

	// Step 1: Load YAML defaults (best-effort; missing file is not an error).
	if err := loadYAML(cfg, yamlPath); err != nil {
		return nil, fmt.Errorf("config: loading yaml %q: %w", yamlPath, err)
	}

	// Step 2: Override with environment variables.
	applyEnv(cfg)

	// Step 3: Apply hard defaults for critical fields.
	if cfg.Port == "" {
		cfg.Port = "3100"
	}
	if cfg.AppEnv == "" {
		cfg.AppEnv = "development"
	}

	return cfg, nil
}

// loadYAML parses yamlPath into cfg. If the file does not exist the function
// returns nil (no error) so callers can rely purely on env vars.
func loadYAML(cfg *Config, path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // missing YAML is fine
		}
		return err
	}
	data = []byte(expandBraceEnv(string(data)))
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing yaml: %w", err)
	}
	return nil
}

func expandBraceEnv(input string) string {
	return braceEnvPattern.ReplaceAllStringFunc(input, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		return os.Getenv(name)
	})
}

// applyEnv overlays environment variables onto cfg for every set variable.
func applyEnv(cfg *Config) {
	for _, m := range envMapping {
		if v, ok := os.LookupEnv(m.envVar); ok {
			m.setter(cfg, v)
		}
	}
}

// DSN returns the database connection string. It prefers DatabaseDSN if set,
// falling back to MySQLDSN for backward compatibility.
func (c *Config) DSN() string {
	if c.DatabaseDSN != "" {
		return c.DatabaseDSN
	}
	return c.MySQLDSN
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
