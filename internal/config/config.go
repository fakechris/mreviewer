// Package config loads application configuration from environment variables
// and a YAML file. Environment variables always take precedence over YAML values.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration values.
type Config struct {
	AppEnv string `yaml:"app_env"`
	Port   string `yaml:"port"`

	MySQLDSN  string `yaml:"mysql_dsn"`
	RedisAddr string `yaml:"redis_addr"`

	GitLabBaseURL       string `yaml:"gitlab_base_url"`
	GitLabToken         string `yaml:"gitlab_token"`
	GitLabWebhookSecret string `yaml:"gitlab_webhook_secret"`

	AnthropicBaseURL string `yaml:"anthropic_base_url"`
	AnthropicAPIKey  string `yaml:"anthropic_api_key"`
	AnthropicModel   string `yaml:"anthropic_model"`
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
	{"REDIS_ADDR", func(c *Config, v string) { c.RedisAddr = v }},
	{"GITLAB_BASE_URL", func(c *Config, v string) { c.GitLabBaseURL = v }},
	{"GITLAB_TOKEN", func(c *Config, v string) { c.GitLabToken = v }},
	{"GITLAB_WEBHOOK_SECRET", func(c *Config, v string) { c.GitLabWebhookSecret = v }},
	{"ANTHROPIC_BASE_URL", func(c *Config, v string) { c.AnthropicBaseURL = v }},
	{"ANTHROPIC_API_KEY", func(c *Config, v string) { c.AnthropicAPIKey = v }},
	{"ANTHROPIC_MODEL", func(c *Config, v string) { c.AnthropicModel = v }},
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
}
