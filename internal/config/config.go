// Package config loads application configuration from environment variables
// and a YAML file. Environment variables always take precedence over YAML values.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultMiniMaxBaseURL = "https://api.minimaxi.com/anthropic"
	defaultMiniMaxModel   = "MiniMax-M2.7-highspeed"
)

// Config holds all application configuration values.
type Config struct {
	AppEnv string `yaml:"app_env"`
	Port   string `yaml:"port"`

	MySQLDSN    string `yaml:"mysql_dsn"`
	DatabaseDSN string `yaml:"database_dsn"`
	RedisAddr   string `yaml:"redis_addr"`

	GitLabBaseURL       string `yaml:"gitlab_base_url"`
	GitLabToken         string `yaml:"gitlab_token"`
	GitLabWebhookSecret string `yaml:"gitlab_webhook_secret"`

	AnthropicBaseURL string `yaml:"anthropic_base_url"`
	AnthropicAPIKey  string `yaml:"anthropic_api_key"`
	AnthropicModel   string `yaml:"anthropic_model"`

	LLM LLMConfig `yaml:"llm"`
}

type LLMConfig struct {
	DefaultRoute  string                    `yaml:"default_route"`
	FallbackRoute string                    `yaml:"fallback_route"`
	Routes        map[string]LLMRouteConfig `yaml:"routes"`
}

type LLMRouteConfig struct {
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

// envMapping maps Config field setters to their environment variable names.
// Each entry is a function that sets the corresponding Config field.
var envMapping = []struct {
	envVar string
	setter func(*Config, string)
}{
	{"APP_ENV", func(c *Config, v string) { c.AppEnv = v }},
	{"PORT", func(c *Config, v string) { c.Port = v }},
	{"MYSQL_DSN", func(c *Config, v string) { c.MySQLDSN = v }},
	{"DATABASE_DSN", func(c *Config, v string) { c.DatabaseDSN = v }},
	{"REDIS_ADDR", func(c *Config, v string) { c.RedisAddr = v }},
	{"GITLAB_BASE_URL", func(c *Config, v string) { c.GitLabBaseURL = v }},
	{"GITLAB_TOKEN", func(c *Config, v string) { c.GitLabToken = v }},
	{"GITLAB_WEBHOOK_SECRET", func(c *Config, v string) { c.GitLabWebhookSecret = v }},
	{"ANTHROPIC_BASE_URL", func(c *Config, v string) { c.AnthropicBaseURL = v }},
	{"ANTHROPIC_API_KEY", func(c *Config, v string) { c.AnthropicAPIKey = v }},
	{"ANTHROPIC_MODEL", func(c *Config, v string) { c.AnthropicModel = v }},
	{"LLM_DEFAULT_ROUTE", func(c *Config, v string) { c.LLM.DefaultRoute = v }},
	{"LLM_FALLBACK_ROUTE", func(c *Config, v string) { c.LLM.FallbackRoute = v }},
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
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing yaml: %w", err)
	}
	return nil
}

// applyEnv overlays environment variables onto cfg for every set variable.
func applyEnv(cfg *Config) {
	for _, m := range envMapping {
		if v, ok := os.LookupEnv(m.envVar); ok {
			m.setter(cfg, v)
		}
	}
	applyMiniMaxFallback(cfg)
}

// DSN returns the database connection string. It prefers DatabaseDSN if set,
// falling back to MySQLDSN for backward compatibility.
func (c *Config) DSN() string {
	if c.DatabaseDSN != "" {
		return c.DatabaseDSN
	}
	return c.MySQLDSN
}

func applyMiniMaxFallback(cfg *Config) {
	if cfg == nil {
		return
	}
	minimaxKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	if minimaxKey == "" {
		return
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		cfg.AnthropicAPIKey = minimaxKey
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")) == "" {
		if baseURL := strings.TrimSpace(os.Getenv("MINIMAX_BASE_URL")); baseURL != "" {
			cfg.AnthropicBaseURL = baseURL
		} else {
			cfg.AnthropicBaseURL = defaultMiniMaxBaseURL
		}
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")) == "" {
		if model := strings.TrimSpace(os.Getenv("MINIMAX_MODEL")); model != "" {
			cfg.AnthropicModel = model
		} else {
			cfg.AnthropicModel = defaultMiniMaxModel
		}
	}
}
