package rules

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
)

// ---------------------------------------------------------------------------
// TestAIReviewYAMLLoad — VAL-BETA-001
// The system reads .gitlab/ai-review.yaml, validates the schema, and applies
// its settings to the effective review config.
// ---------------------------------------------------------------------------
func TestAIReviewYAMLLoad(t *testing.T) {
	const aiReviewYAML = `
enabled: true
confidence_threshold: 0.85
severity_threshold: high
include_paths:
  - "cmd/**"
  - "internal/**"
exclude_paths:
  - "testdata/**"
gate_mode: external_status
provider_route: minimax-enterprise
max_files: 50
max_changed_lines: 1500
context_lines_before: 25
context_lines_after: 15
`

	t.Run("valid_yaml_applied_to_effective_config", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": aiReviewYAML,
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		ep := result.EffectivePolicy
		if ep.ConfidenceThreshold != 0.85 {
			t.Errorf("ConfidenceThreshold = %v, want 0.85", ep.ConfidenceThreshold)
		}
		if ep.SeverityThreshold != "high" {
			t.Errorf("SeverityThreshold = %q, want high", ep.SeverityThreshold)
		}
		if !reflect.DeepEqual(ep.IncludePaths, []string{"cmd/**", "internal/**"}) {
			t.Errorf("IncludePaths = %v, want [cmd/** internal/**]", ep.IncludePaths)
		}
		if !reflect.DeepEqual(ep.ExcludePaths, []string{"testdata/**"}) {
			t.Errorf("ExcludePaths = %v, want [testdata/**]", ep.ExcludePaths)
		}
		if ep.GateMode != "external_status" {
			t.Errorf("GateMode = %q, want external_status", ep.GateMode)
		}
		if ep.ProviderRoute != "minimax-enterprise" {
			t.Errorf("ProviderRoute = %q, want minimax-enterprise", ep.ProviderRoute)
		}
		if ep.MaxFiles != 50 {
			t.Errorf("MaxFiles = %d, want 50", ep.MaxFiles)
		}
		if ep.MaxChangedLines != 1500 {
			t.Errorf("MaxChangedLines = %d, want 1500", ep.MaxChangedLines)
		}
		if ep.ContextLinesBefore != 25 {
			t.Errorf("ContextLinesBefore = %d, want 25", ep.ContextLinesBefore)
		}
		if ep.ContextLinesAfter != 15 {
			t.Errorf("ContextLinesAfter = %d, want 15", ep.ContextLinesAfter)
		}
	})

	t.Run("missing_yaml_uses_defaults", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{}} // no ai-review.yaml
		defaults := defaultPlatformDefaults()
		loader := NewLoader(reader, defaults)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		// Should match platform defaults.
		if result.EffectivePolicy.ConfidenceThreshold != defaults.ConfidenceThreshold {
			t.Errorf("ConfidenceThreshold = %v, want %v", result.EffectivePolicy.ConfidenceThreshold, defaults.ConfidenceThreshold)
		}
	})

	t.Run("yaml_overrides_project_policy", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": "confidence_threshold: 0.95\nseverity_threshold: high\n",
		}}
		project := &db.ProjectPolicy{
			ConfidenceThreshold: 0.80,
			SeverityThreshold:   "medium",
		}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			ProjectPolicy: project,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		// ai-review.yaml should override project policy.
		if result.EffectivePolicy.ConfidenceThreshold != 0.95 {
			t.Errorf("ConfidenceThreshold = %v, want 0.95 (from ai-review.yaml)", result.EffectivePolicy.ConfidenceThreshold)
		}
		if result.EffectivePolicy.SeverityThreshold != "high" {
			t.Errorf("SeverityThreshold = %q, want high (from ai-review.yaml)", result.EffectivePolicy.SeverityThreshold)
		}
	})

	t.Run("parse_known_fields_only", func(t *testing.T) {
		cfg, warnings, err := ParseAIReviewConfig("confidence_threshold: 0.9\nunknown_field: ignored\n")
		if err != nil {
			t.Fatalf("ParseAIReviewConfig: %v", err)
		}
		if cfg == nil || cfg.ConfidenceThreshold == nil || *cfg.ConfidenceThreshold != 0.9 {
			t.Errorf("confidence_threshold not parsed correctly")
		}
		// Unknown fields should not produce warnings (YAML unmarshal is lenient).
		_ = warnings
	})
}

// ---------------------------------------------------------------------------
// TestInvalidAIReviewYAMLFallback — VAL-BETA-002
// Invalid .gitlab/ai-review.yaml falls back to defaults without aborting.
// ---------------------------------------------------------------------------
func TestInvalidAIReviewYAMLFallback(t *testing.T) {
	t.Run("malformed_yaml_syntax", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": "{{invalid yaml[[[",
		}}
		defaults := defaultPlatformDefaults()
		loader := NewLoader(reader, defaults)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
		})
		if err != nil {
			t.Fatalf("Load should not fail on invalid YAML, got: %v", err)
		}
		// Should fall back to platform defaults.
		if result.EffectivePolicy.ConfidenceThreshold != defaults.ConfidenceThreshold {
			t.Errorf("ConfidenceThreshold = %v, want default %v", result.EffectivePolicy.ConfidenceThreshold, defaults.ConfidenceThreshold)
		}
	})

	t.Run("out_of_range_confidence_ignored", func(t *testing.T) {
		cfg, warnings, _ := ParseAIReviewConfig("confidence_threshold: 1.5\n")
		if cfg != nil && cfg.ConfidenceThreshold != nil {
			t.Errorf("out-of-range confidence_threshold should be nil'd out")
		}
		if len(warnings) == 0 {
			t.Error("expected a warning for out-of-range confidence_threshold")
		}
	})

	t.Run("unknown_severity_ignored", func(t *testing.T) {
		cfg, warnings, _ := ParseAIReviewConfig("severity_threshold: critical\n")
		if cfg != nil && cfg.SeverityThreshold != nil {
			t.Errorf("unknown severity_threshold should be nil'd out")
		}
		if len(warnings) == 0 {
			t.Error("expected a warning for unknown severity_threshold")
		}
	})

	t.Run("unknown_gate_mode_ignored", func(t *testing.T) {
		cfg, warnings, _ := ParseAIReviewConfig("gate_mode: foobar\n")
		if cfg != nil && cfg.GateMode != nil {
			t.Errorf("unknown gate_mode should be nil'd out")
		}
		if len(warnings) == 0 {
			t.Error("expected a warning for unknown gate_mode")
		}
	})

	t.Run("negative_max_files_ignored", func(t *testing.T) {
		cfg, warnings, _ := ParseAIReviewConfig("max_files: -5\n")
		if cfg != nil && cfg.MaxFiles != nil {
			t.Errorf("negative max_files should be nil'd out")
		}
		if len(warnings) == 0 {
			t.Error("expected a warning for negative max_files")
		}
	})

	t.Run("empty_yaml_is_noop", func(t *testing.T) {
		cfg, warnings, err := ParseAIReviewConfig("")
		if err != nil {
			t.Fatalf("ParseAIReviewConfig: %v", err)
		}
		if cfg != nil {
			t.Errorf("empty YAML should return nil config")
		}
		if len(warnings) > 0 {
			t.Errorf("empty YAML should not produce warnings")
		}
	})

	t.Run("run_not_aborted_on_invalid_yaml", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": ": : :\n  broken: [unterminated",
			"REVIEW.md@head-sha":              "# Custom review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
		})
		if err != nil {
			t.Fatalf("Load should not fail on invalid ai-review.yaml, got: %v", err)
		}
		// Root REVIEW.md should still be loaded.
		if result.Trusted.ReviewMarkdown != "# Custom review\n" {
			t.Errorf("ReviewMarkdown = %q, want '# Custom review\\n'", result.Trusted.ReviewMarkdown)
		}
	})
}

// ---------------------------------------------------------------------------
// TestDirectoryScopedReviewPriority — VAL-BETA-003
// Directory-scoped REVIEW.md overrides root REVIEW.md for matching changed
// files.
// ---------------------------------------------------------------------------
func TestDirectoryScopedReviewPriority(t *testing.T) {
	t.Run("nested_review_overrides_root", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha":          "# Root review guidance\n",
			"src/auth/REVIEW.md@head-sha": "# Auth-specific review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:    123,
			HeadSHA:      "head-sha",
			ChangedPaths: []string{"src/auth/login.go", "src/auth/session.go"},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.Trusted.ReviewMarkdown != "# Auth-specific review\n" {
			t.Errorf("ReviewMarkdown = %q, want auth-specific review", result.Trusted.ReviewMarkdown)
		}
		if !strings.Contains(result.SystemPrompt, "Auth-specific review") {
			t.Errorf("system prompt should contain auth-specific review")
		}
	})

	t.Run("deepest_directory_wins", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha":                "# Root review\n",
			"src/REVIEW.md@head-sha":            "# Src review\n",
			"src/auth/REVIEW.md@head-sha":       "# Auth review\n",
			"src/auth/oauth/REVIEW.md@head-sha": "# OAuth review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:    123,
			HeadSHA:      "head-sha",
			ChangedPaths: []string{"src/auth/oauth/handler.go"},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.Trusted.ReviewMarkdown != "# OAuth review\n" {
			t.Errorf("ReviewMarkdown = %q, want deepest OAuth review", result.Trusted.ReviewMarkdown)
		}
	})

	t.Run("no_directory_review_falls_back_to_root", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha": "# Root review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:    123,
			HeadSHA:      "head-sha",
			ChangedPaths: []string{"src/main.go"},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.Trusted.ReviewMarkdown != "# Root review\n" {
			t.Errorf("ReviewMarkdown = %q, want root review", result.Trusted.ReviewMarkdown)
		}
	})

	t.Run("no_changed_paths_uses_root_only", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha":          "# Root review\n",
			"src/auth/REVIEW.md@head-sha": "# Auth review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
			// No ChangedPaths - should use root only.
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.Trusted.ReviewMarkdown != "# Root review\n" {
			t.Errorf("ReviewMarkdown = %q, want root review when no changed paths", result.Trusted.ReviewMarkdown)
		}
	})

	t.Run("multiple_changed_dirs_picks_deepest_review", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha":          "# Root\n",
			"pkg/REVIEW.md@head-sha":      "# Pkg review\n",
			"src/auth/REVIEW.md@head-sha": "# Auth review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:    123,
			HeadSHA:      "head-sha",
			ChangedPaths: []string{"pkg/util.go", "src/auth/login.go"},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		// src/auth is deeper than pkg, so auth review should win.
		if result.Trusted.ReviewMarkdown != "# Auth review\n" {
			t.Errorf("ReviewMarkdown = %q, want auth review (deeper)", result.Trusted.ReviewMarkdown)
		}
	})
}

// ---------------------------------------------------------------------------
// TestConfigLayerPrecedence — VAL-BETA-004
// Config resolution order:
//
//	platform defaults < group policy < project policy < ai-review.yaml < REVIEW.md
//
// ---------------------------------------------------------------------------
func TestConfigLayerPrecedence(t *testing.T) {
	t.Run("full_precedence_chain", func(t *testing.T) {
		// Setup: each layer overrides different values to verify ordering.
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": "confidence_threshold: 0.95\ngate_mode: ci\n",
			"REVIEW.md@head-sha":              "# Final trusted instructions\n",
		}}

		platform := PlatformDefaults{
			Instructions:        "Platform instructions",
			ConfidenceThreshold: 0.60,
			SeverityThreshold:   "low",
			IncludePaths:        []string{"**"},
			ExcludePaths:        []string{"vendor/**"},
			GateMode:            "disabled",
			ProviderRoute:       "platform-default",
		}

		group := &GroupPolicy{
			ConfidenceThreshold: 0.70,
			SeverityThreshold:   "medium",
			GateMode:            "threads_resolved",
			ProviderRoute:       "group-route",
		}

		project := &db.ProjectPolicy{
			ConfidenceThreshold: 0.80,
			SeverityThreshold:   "high",
			ProviderRoute:       "project-route",
		}

		loader := NewLoader(reader, platform)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			GroupPolicy:   group,
			ProjectPolicy: project,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		ep := result.EffectivePolicy

		// confidence_threshold: ai-review.yaml (0.95) > project (0.80) > group (0.70) > platform (0.60)
		if ep.ConfidenceThreshold != 0.95 {
			t.Errorf("ConfidenceThreshold = %v, want 0.95 (from ai-review.yaml)", ep.ConfidenceThreshold)
		}

		// severity_threshold: project (high) > group (medium) > platform (low);
		// ai-review.yaml doesn't set it, so project wins.
		if ep.SeverityThreshold != "high" {
			t.Errorf("SeverityThreshold = %q, want high (from project policy)", ep.SeverityThreshold)
		}

		// gate_mode: ai-review.yaml (ci) > project (unset) > group (threads_resolved) > platform (disabled)
		if ep.GateMode != "ci" {
			t.Errorf("GateMode = %q, want ci (from ai-review.yaml)", ep.GateMode)
		}

		// provider_route: project (project-route) > group (group-route) > platform (platform-default);
		// ai-review.yaml doesn't set it, so project wins.
		if ep.ProviderRoute != "project-route" {
			t.Errorf("ProviderRoute = %q, want project-route", ep.ProviderRoute)
		}

		// REVIEW.md should appear as trusted instructions (highest precedence for trusted text).
		if result.Trusted.ReviewMarkdown != "# Final trusted instructions\n" {
			t.Errorf("ReviewMarkdown = %q, want final trusted instructions", result.Trusted.ReviewMarkdown)
		}
		if !strings.Contains(result.SystemPrompt, "Final trusted instructions") {
			t.Errorf("system prompt should contain REVIEW.md content")
		}
	})

	t.Run("group_overrides_platform", func(t *testing.T) {
		reader := stubFileReader{}
		platform := PlatformDefaults{
			ConfidenceThreshold: 0.50,
			SeverityThreshold:   "low",
			GateMode:            "disabled",
			ProviderRoute:       "default",
		}
		group := &GroupPolicy{
			ConfidenceThreshold: 0.75,
			SeverityThreshold:   "medium",
			GateMode:            "threads_resolved",
			ProviderRoute:       "group-route",
		}
		loader := NewLoader(reader, platform)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:   123,
			HeadSHA:     "head-sha",
			GroupPolicy: group,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.EffectivePolicy.ConfidenceThreshold != 0.75 {
			t.Errorf("ConfidenceThreshold = %v, want 0.75 (from group)", result.EffectivePolicy.ConfidenceThreshold)
		}
		if result.EffectivePolicy.SeverityThreshold != "medium" {
			t.Errorf("SeverityThreshold = %q, want medium (from group)", result.EffectivePolicy.SeverityThreshold)
		}
		if result.EffectivePolicy.GateMode != "threads_resolved" {
			t.Errorf("GateMode = %q, want threads_resolved (from group)", result.EffectivePolicy.GateMode)
		}
	})

	t.Run("project_overrides_group", func(t *testing.T) {
		reader := stubFileReader{}
		platform := PlatformDefaults{
			ConfidenceThreshold: 0.50,
			SeverityThreshold:   "low",
			GateMode:            "disabled",
		}
		group := &GroupPolicy{
			ConfidenceThreshold: 0.70,
			SeverityThreshold:   "medium",
			GateMode:            "threads_resolved",
			IncludePaths:        []string{"group/**"},
		}
		project := &db.ProjectPolicy{
			ConfidenceThreshold: 0.85,
			GateMode:            "external_status",
		}
		loader := NewLoader(reader, platform)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			GroupPolicy:   group,
			ProjectPolicy: project,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		// project overrides group confidence.
		if result.EffectivePolicy.ConfidenceThreshold != 0.85 {
			t.Errorf("ConfidenceThreshold = %v, want 0.85 (from project)", result.EffectivePolicy.ConfidenceThreshold)
		}
		// project overrides group gate mode.
		if result.EffectivePolicy.GateMode != "external_status" {
			t.Errorf("GateMode = %q, want external_status (from project)", result.EffectivePolicy.GateMode)
		}
		// project doesn't set severity, so group wins over platform.
		if result.EffectivePolicy.SeverityThreshold != "medium" {
			t.Errorf("SeverityThreshold = %q, want medium (from group)", result.EffectivePolicy.SeverityThreshold)
		}
	})

	t.Run("ai_review_yaml_overrides_project", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@head-sha": "confidence_threshold: 0.99\n",
		}}
		project := &db.ProjectPolicy{
			ConfidenceThreshold: 0.80,
		}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			ProjectPolicy: project,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if result.EffectivePolicy.ConfidenceThreshold != 0.99 {
			t.Errorf("ConfidenceThreshold = %v, want 0.99 (ai-review.yaml > project)", result.EffectivePolicy.ConfidenceThreshold)
		}
	})

	t.Run("review_md_overrides_all_for_trusted_text", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			"REVIEW.md@head-sha": "# Top-level trusted review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "head-sha",
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if !strings.Contains(result.SystemPrompt, "Top-level trusted review") {
			t.Errorf("system prompt should contain REVIEW.md content at highest precedence")
		}
	})

	t.Run("group_include_paths_override_platform", func(t *testing.T) {
		reader := stubFileReader{}
		platform := PlatformDefaults{
			IncludePaths: []string{"src/**"},
			ExcludePaths: []string{"vendor/**"},
		}
		group := &GroupPolicy{
			IncludePaths: []string{"internal/**", "cmd/**"},
		}
		loader := NewLoader(reader, platform)

		result, err := loader.Load(context.Background(), LoadInput{
			ProjectID:   123,
			HeadSHA:     "head-sha",
			GroupPolicy: group,
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if !reflect.DeepEqual(result.EffectivePolicy.IncludePaths, []string{"internal/**", "cmd/**"}) {
			t.Errorf("IncludePaths = %v, want group's paths", result.EffectivePolicy.IncludePaths)
		}
		// ExcludePaths should be from platform since group didn't set them.
		if !reflect.DeepEqual(result.EffectivePolicy.ExcludePaths, []string{"vendor/**"}) {
			t.Errorf("ExcludePaths = %v, want platform's paths", result.EffectivePolicy.ExcludePaths)
		}
	})
}

// ---------------------------------------------------------------------------
// TestConfigChangeAffectsNextRun — VAL-CROSS-010
// Changed policy/config affects the next run and does not retroactively alter
// prior runs. This is verified by showing that each Load() call uses the
// current inputs (head_sha, policy) independently.
// ---------------------------------------------------------------------------
func TestConfigChangeAffectsNextRun(t *testing.T) {
	t.Run("different_head_sha_loads_different_config", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@sha-v1": "confidence_threshold: 0.70\n",
			".gitlab/ai-review.yaml@sha-v2": "confidence_threshold: 0.95\n",
			"REVIEW.md@sha-v1":              "# V1 review\n",
			"REVIEW.md@sha-v2":              "# V2 review\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		// Run 1 with sha-v1.
		r1, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "sha-v1",
		})
		if err != nil {
			t.Fatalf("Load v1: %v", err)
		}

		// Run 2 with sha-v2 (changed config).
		r2, err := loader.Load(context.Background(), LoadInput{
			ProjectID: 123,
			HeadSHA:   "sha-v2",
		})
		if err != nil {
			t.Fatalf("Load v2: %v", err)
		}

		// V1 should have 0.70.
		if r1.EffectivePolicy.ConfidenceThreshold != 0.70 {
			t.Errorf("v1 ConfidenceThreshold = %v, want 0.70", r1.EffectivePolicy.ConfidenceThreshold)
		}
		// V2 should have 0.95.
		if r2.EffectivePolicy.ConfidenceThreshold != 0.95 {
			t.Errorf("v2 ConfidenceThreshold = %v, want 0.95", r2.EffectivePolicy.ConfidenceThreshold)
		}

		// REVIEW.md content should differ.
		if r1.Trusted.ReviewMarkdown != "# V1 review\n" {
			t.Errorf("v1 ReviewMarkdown = %q, want v1", r1.Trusted.ReviewMarkdown)
		}
		if r2.Trusted.ReviewMarkdown != "# V2 review\n" {
			t.Errorf("v2 ReviewMarkdown = %q, want v2", r2.Trusted.ReviewMarkdown)
		}

		// Rules digests should differ (different config = different digest).
		if r1.Trusted.RulesDigest == r2.Trusted.RulesDigest {
			t.Errorf("RulesDigest should differ between v1 and v2")
		}
	})

	t.Run("changed_project_policy_affects_next_run", func(t *testing.T) {
		reader := stubFileReader{}
		loader := NewLoader(reader, defaultPlatformDefaults())

		// Run 1 with low threshold.
		policyV1 := &db.ProjectPolicy{ConfidenceThreshold: 0.50}
		r1, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			ProjectPolicy: policyV1,
		})
		if err != nil {
			t.Fatalf("Load v1: %v", err)
		}

		// Run 2 with higher threshold (simulates policy update between runs).
		policyV2 := &db.ProjectPolicy{ConfidenceThreshold: 0.90}
		r2, err := loader.Load(context.Background(), LoadInput{
			ProjectID:     123,
			HeadSHA:       "head-sha",
			ProjectPolicy: policyV2,
		})
		if err != nil {
			t.Fatalf("Load v2: %v", err)
		}

		if r1.EffectivePolicy.ConfidenceThreshold != 0.50 {
			t.Errorf("v1 ConfidenceThreshold = %v, want 0.50", r1.EffectivePolicy.ConfidenceThreshold)
		}
		if r2.EffectivePolicy.ConfidenceThreshold != 0.90 {
			t.Errorf("v2 ConfidenceThreshold = %v, want 0.90", r2.EffectivePolicy.ConfidenceThreshold)
		}
	})

	t.Run("changed_group_policy_affects_next_run", func(t *testing.T) {
		reader := stubFileReader{}
		loader := NewLoader(reader, defaultPlatformDefaults())

		groupV1 := &GroupPolicy{ConfidenceThreshold: 0.60, GateMode: "disabled"}
		r1, err := loader.Load(context.Background(), LoadInput{
			ProjectID:   123,
			HeadSHA:     "head-sha",
			GroupPolicy: groupV1,
		})
		if err != nil {
			t.Fatalf("Load v1: %v", err)
		}

		groupV2 := &GroupPolicy{ConfidenceThreshold: 0.85, GateMode: "threads_resolved"}
		r2, err := loader.Load(context.Background(), LoadInput{
			ProjectID:   123,
			HeadSHA:     "head-sha",
			GroupPolicy: groupV2,
		})
		if err != nil {
			t.Fatalf("Load v2: %v", err)
		}

		if r1.EffectivePolicy.ConfidenceThreshold != 0.60 {
			t.Errorf("v1 ConfidenceThreshold = %v, want 0.60", r1.EffectivePolicy.ConfidenceThreshold)
		}
		if r2.EffectivePolicy.ConfidenceThreshold != 0.85 {
			t.Errorf("v2 ConfidenceThreshold = %v, want 0.85", r2.EffectivePolicy.ConfidenceThreshold)
		}
		if r1.EffectivePolicy.GateMode != "disabled" {
			t.Errorf("v1 GateMode = %q, want disabled", r1.EffectivePolicy.GateMode)
		}
		if r2.EffectivePolicy.GateMode != "threads_resolved" {
			t.Errorf("v2 GateMode = %q, want threads_resolved", r2.EffectivePolicy.GateMode)
		}
	})

	t.Run("loader_is_stateless_between_calls", func(t *testing.T) {
		reader := stubFileReader{content: map[string]string{
			".gitlab/ai-review.yaml@sha-a": "confidence_threshold: 0.55\n",
			".gitlab/ai-review.yaml@sha-b": "confidence_threshold: 0.99\n",
		}}
		loader := NewLoader(reader, defaultPlatformDefaults())

		// First call.
		r1, _ := loader.Load(context.Background(), LoadInput{ProjectID: 1, HeadSHA: "sha-a"})
		// Second call.
		r2, _ := loader.Load(context.Background(), LoadInput{ProjectID: 1, HeadSHA: "sha-b"})
		// Re-call with first SHA to prove no state leaks.
		r3, _ := loader.Load(context.Background(), LoadInput{ProjectID: 1, HeadSHA: "sha-a"})

		if r1.EffectivePolicy.ConfidenceThreshold != 0.55 {
			t.Errorf("r1 = %v, want 0.55", r1.EffectivePolicy.ConfidenceThreshold)
		}
		if r2.EffectivePolicy.ConfidenceThreshold != 0.99 {
			t.Errorf("r2 = %v, want 0.99", r2.EffectivePolicy.ConfidenceThreshold)
		}
		if r3.EffectivePolicy.ConfidenceThreshold != 0.55 {
			t.Errorf("r3 = %v, want 0.55 (same as r1, no state leak)", r3.EffectivePolicy.ConfidenceThreshold)
		}
	})
}

// ---------------------------------------------------------------------------
// Helper: directoryReviewCandidates unit test.
// ---------------------------------------------------------------------------
func TestDirectoryReviewCandidates(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  []string
	}{
		{
			name:  "single_deep_path",
			paths: []string{"src/auth/oauth/handler.go"},
			want:  []string{"src/auth/oauth", "src/auth", "src"},
		},
		{
			name:  "root_level_file",
			paths: []string{"main.go"},
			want:  nil,
		},
		{
			name:  "multiple_paths_with_shared_ancestor",
			paths: []string{"src/auth/login.go", "src/auth/session.go"},
			want:  []string{"src/auth", "src"},
		},
		{
			name:  "deduplication",
			paths: []string{"pkg/a/file1.go", "pkg/a/file2.go", "pkg/b/file3.go"},
			want:  []string{"pkg/a", "pkg/b", "pkg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := directoryReviewCandidates(tc.paths)
			if len(tc.want) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("directoryReviewCandidates(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}
}

// Compile-time assertions.
var _ RepositoryFileReader = stubFileReader{}
var _ RepositoryFileReader = (*gitlab.Client)(nil)
