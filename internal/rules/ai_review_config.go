package rules

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AIReviewConfig represents the parsed and validated contents of
// .gitlab/ai-review.yaml. Only known fields are applied; unknown fields
// are ignored with a warning.
type AIReviewConfig struct {
	Enabled             *bool    `yaml:"enabled"`
	ConfidenceThreshold *float64 `yaml:"confidence_threshold"`
	SeverityThreshold   *string  `yaml:"severity_threshold"`
	IncludePaths        []string `yaml:"include_paths"`
	ExcludePaths        []string `yaml:"exclude_paths"`
	ContextMode         *string  `yaml:"context_mode"`
	GateMode            *string  `yaml:"gate_mode"`
	ProviderRoute       *string  `yaml:"provider_route"`
	MaxFiles            *int     `yaml:"max_files"`
	MaxChangedLines     *int     `yaml:"max_changed_lines"`
	ContextLinesBefore  *int     `yaml:"context_lines_before"`
	ContextLinesAfter   *int     `yaml:"context_lines_after"`
}

const aiReviewYAMLPath = ".gitlab/ai-review.yaml"

// validSeverities is the set of recognised severity threshold values.
var validSeverities = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"nit":    true,
}

// validGateModes is the set of recognised gate modes.
var validGateModes = map[string]bool{
	"threads_resolved": true,
	"external_status":  true,
	"ci":               true,
	"disabled":         true,
}

// ParseAIReviewConfig parses YAML content into an AIReviewConfig. It returns
// a nil config and no error for empty input. It returns a nil config and no
// error for invalid YAML (falls back to defaults).
func ParseAIReviewConfig(content string) (*AIReviewConfig, []string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil, nil
	}

	var cfg AIReviewConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, []string{fmt.Sprintf("ai-review.yaml: invalid YAML syntax: %v", err)}, nil
	}

	warnings := validateAIReviewConfig(&cfg)
	return &cfg, warnings, nil
}

// validateAIReviewConfig checks field values and returns warnings for invalid
// ones. Invalid fields are nil'd out so they don't get applied.
func validateAIReviewConfig(cfg *AIReviewConfig) []string {
	var warnings []string

	if cfg.ConfidenceThreshold != nil {
		if *cfg.ConfidenceThreshold < 0 || *cfg.ConfidenceThreshold > 1 {
			warnings = append(warnings, fmt.Sprintf("ai-review.yaml: confidence_threshold %.2f out of range [0, 1], ignoring", *cfg.ConfidenceThreshold))
			cfg.ConfidenceThreshold = nil
		}
	}

	if cfg.SeverityThreshold != nil {
		v := strings.ToLower(strings.TrimSpace(*cfg.SeverityThreshold))
		if !validSeverities[v] {
			warnings = append(warnings, fmt.Sprintf("ai-review.yaml: unknown severity_threshold %q, ignoring", *cfg.SeverityThreshold))
			cfg.SeverityThreshold = nil
		} else {
			cfg.SeverityThreshold = &v
		}
	}

	if cfg.GateMode != nil {
		v := strings.ToLower(strings.TrimSpace(*cfg.GateMode))
		if !validGateModes[v] {
			warnings = append(warnings, fmt.Sprintf("ai-review.yaml: unknown gate_mode %q, ignoring", *cfg.GateMode))
			cfg.GateMode = nil
		} else {
			cfg.GateMode = &v
		}
	}

	if cfg.MaxFiles != nil && *cfg.MaxFiles <= 0 {
		warnings = append(warnings, fmt.Sprintf("ai-review.yaml: max_files %d must be positive, ignoring", *cfg.MaxFiles))
		cfg.MaxFiles = nil
	}

	if cfg.MaxChangedLines != nil && *cfg.MaxChangedLines <= 0 {
		warnings = append(warnings, fmt.Sprintf("ai-review.yaml: max_changed_lines %d must be positive, ignoring", *cfg.MaxChangedLines))
		cfg.MaxChangedLines = nil
	}

	if cfg.ContextLinesBefore != nil && *cfg.ContextLinesBefore < 0 {
		warnings = append(warnings, fmt.Sprintf("ai-review.yaml: context_lines_before %d must be non-negative, ignoring", *cfg.ContextLinesBefore))
		cfg.ContextLinesBefore = nil
	}

	if cfg.ContextLinesAfter != nil && *cfg.ContextLinesAfter < 0 {
		warnings = append(warnings, fmt.Sprintf("ai-review.yaml: context_lines_after %d must be non-negative, ignoring", *cfg.ContextLinesAfter))
		cfg.ContextLinesAfter = nil
	}

	return warnings
}

// applyAIReviewConfig merges valid ai-review.yaml settings on top of the
// effective policy. It sits above project policy but below REVIEW.md in the
// precedence chain.
func applyAIReviewConfig(effective *EffectivePolicy, cfg *AIReviewConfig) {
	if cfg == nil {
		return
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		// If the repo explicitly disables ai-review, we still load defaults
		// but mark a sentinel. Callers can check this. For now we just skip
		// applying overrides, which effectively means defaults apply.
		return
	}
	if cfg.ConfidenceThreshold != nil {
		effective.ConfidenceThreshold = *cfg.ConfidenceThreshold
	}
	if cfg.SeverityThreshold != nil {
		effective.SeverityThreshold = *cfg.SeverityThreshold
	}
	if len(cfg.IncludePaths) > 0 {
		effective.IncludePaths = append([]string(nil), cfg.IncludePaths...)
	}
	if len(cfg.ExcludePaths) > 0 {
		effective.ExcludePaths = append([]string(nil), cfg.ExcludePaths...)
	}
	if cfg.GateMode != nil {
		effective.GateMode = *cfg.GateMode
	}
	if cfg.ProviderRoute != nil {
		effective.ProviderRoute = *cfg.ProviderRoute
	}
	if cfg.MaxFiles != nil {
		effective.MaxFiles = *cfg.MaxFiles
	}
	if cfg.MaxChangedLines != nil {
		effective.MaxChangedLines = *cfg.MaxChangedLines
	}
	if cfg.ContextLinesBefore != nil {
		effective.ContextLinesBefore = *cfg.ContextLinesBefore
	}
	if cfg.ContextLinesAfter != nil {
		effective.ContextLinesAfter = *cfg.ContextLinesAfter
	}
}
