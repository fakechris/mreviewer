package reviewpack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/llm"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

const syntheticAcceptanceEvidencePath = "testdata/synthetic_acceptance/runtime_evidence.json"

type syntheticScenario struct {
	Name             string
	PackID           string
	Title            string
	Description      string
	ExpectedKeywords []string
	Changes          []ctxpkg.Change
}

type syntheticAcceptanceResult struct {
	Name          string         `json:"name"`
	PackID        string         `json:"pack_id"`
	Summary       string         `json:"summary,omitempty"`
	FindingCount  int            `json:"finding_count"`
	Matched       bool           `json:"matched"`
	MatchedOn     []string       `json:"matched_on,omitempty"`
	Artifact      any            `json:"artifact,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
}

type syntheticAcceptanceEvidence struct {
	Route   string                      `json:"route"`
	Results []syntheticAcceptanceResult `json:"results"`
}

func TestSyntheticAcceptanceCorpus(t *testing.T) {
	t.Parallel()

	corpus := syntheticAcceptanceCorpus()
	if len(corpus) < 6 {
		t.Fatalf("synthetic corpus len = %d, want >= 6", len(corpus))
	}

	perPack := map[string]int{}
	for _, scenario := range corpus {
		if strings.TrimSpace(scenario.Name) == "" {
			t.Fatalf("scenario with empty name: %+v", scenario)
		}
		if strings.TrimSpace(scenario.PackID) == "" {
			t.Fatalf("scenario %q has empty pack id", scenario.Name)
		}
		if len(scenario.ExpectedKeywords) == 0 {
			t.Fatalf("scenario %q missing expected keywords", scenario.Name)
		}
		if len(scenario.Changes) == 0 {
			t.Fatalf("scenario %q missing changes", scenario.Name)
		}
		perPack[scenario.PackID]++
	}

	if perPack["security"] < 3 {
		t.Fatalf("security scenarios = %d, want >= 3", perPack["security"])
	}
	if perPack["database"] < 3 {
		t.Fatalf("database scenarios = %d, want >= 3", perPack["database"])
	}
}

func TestRunSyntheticPackAcceptance(t *testing.T) {
	if os.Getenv("MREVIEWER_RUN_SYNTHETIC_PACK_ACCEPTANCE") != "1" {
		t.Skip("set MREVIEWER_RUN_SYNTHETIC_PACK_ACCEPTANCE=1 to run real-provider synthetic pack acceptance")
	}

	cfg, err := config.Load("config.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	defaultRoute, fallbackRoute, providerConfigs, err := providerConfigsFromConfigForAcceptance(cfg)
	if err != nil {
		t.Fatalf("provider configs: %v", err)
	}

	registry, err := llm.BuildProviderRegistryFromRouteConfigs(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultRoute,
		fallbackRoute,
		providerConfigs,
	)
	if err != nil {
		t.Fatalf("build provider registry: %v", err)
	}

	var evidence syntheticAcceptanceEvidence
	evidence.Route = defaultRoute

	for _, scenario := range syntheticAcceptanceCorpus() {
		t.Run(scenario.Name, func(t *testing.T) {
			result := runSyntheticScenario(t, registry, defaultRoute, scenario)
			evidence.Results = append(evidence.Results, result)
			if result.FailureReason != "" {
				t.Fatalf("%s", result.FailureReason)
			}
		})
	}

	if os.Getenv("MREVIEWER_WRITE_SYNTHETIC_PACK_EVIDENCE") == "1" {
		if err := writeSyntheticAcceptanceEvidence(evidence); err != nil {
			t.Fatalf("write synthetic acceptance evidence: %v", err)
		}
	}
}

func runSyntheticScenario(t *testing.T, registry *llm.ProviderRegistry, defaultRoute string, scenario syntheticScenario) syntheticAcceptanceResult {
	t.Helper()

	contract, ok := findDefaultPackContract(scenario.PackID)
	if !ok {
		return syntheticAcceptanceResult{
			Name:          scenario.Name,
			PackID:        scenario.PackID,
			FailureReason: fmt.Sprintf("missing pack contract %q", scenario.PackID),
		}
	}

	request := ctxpkg.ReviewRequest{
		SchemaVersion: "v1",
		ReviewRunID:   "synthetic-" + scenario.Name,
		Project: ctxpkg.ProjectContext{
			ProjectID:     1,
			FullPath:      "synthetic/reviewpack",
			DefaultBranch: "main",
		},
		MergeRequest: ctxpkg.MergeRequestContext{
			IID:         1,
			Title:       scenario.Title,
			Description: scenario.Description,
			Author:      "synthetic",
		},
		Rules: ctxpkg.TrustedRules{
			PlatformPolicy: "Synthetic acceptance corpus for specialist reviewer calibration.",
			ProjectPolicy:  "Report only new issues from changed hunks.",
		},
		Changes: scenario.Changes,
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return syntheticAcceptanceResult{
			Name:          scenario.Name,
			PackID:        scenario.PackID,
			FailureReason: fmt.Sprintf("marshal request: %v", err),
		}
	}

	runner := NewLegacyResolverRunner(contract, registry.ResolveWithFallback)
	artifact, err := runner.Run(context.Background(), core.ReviewInput{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "synthetic://reviewpack/" + scenario.Name,
			Repository:   "synthetic/reviewpack",
			ChangeNumber: 1,
		},
		Request:        request,
		RequestPayload: payload,
		SystemPrompt:   "Follow trusted instructions and return only valid JSON matching the review schema.",
		EffectivePolicy: rules.EffectivePolicy{
			ProviderRoute: defaultRoute,
		},
		Metadata: map[string]string{
			"provider_route": defaultRoute,
		},
	}, core.RunOptions{ReviewerPacks: []string{scenario.PackID}})
	if err != nil {
		return syntheticAcceptanceResult{
			Name:          scenario.Name,
			PackID:        scenario.PackID,
			FailureReason: fmt.Sprintf("run reviewer pack: %v", err),
		}
	}

	result := syntheticAcceptanceResult{
		Name:         scenario.Name,
		PackID:       scenario.PackID,
		Summary:      artifact.Summary,
		FindingCount: len(artifact.Findings),
		Artifact:     artifact,
	}
	if len(artifact.Findings) == 0 {
		result.FailureReason = "finding_count = 0, want >= 1"
		return result
	}

	result.Matched, result.MatchedOn = syntheticFindingMatch(artifact, scenario.ExpectedKeywords)
	if !result.Matched {
		result.FailureReason = fmt.Sprintf("findings did not match expected keywords: %s", strings.Join(scenario.ExpectedKeywords, ", "))
	}
	return result
}

func syntheticFindingMatch(artifact core.ReviewerArtifact, expectedKeywords []string) (bool, []string) {
	if len(expectedKeywords) == 0 {
		return true, nil
	}

	text := strings.ToLower(strings.Join([]string{
		artifact.Summary,
		findingsText(artifact.Findings),
	}, "\n"))

	var matched []string
	for _, keyword := range expectedKeywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if strings.Contains(text, keyword) {
			matched = append(matched, keyword)
		}
	}
	return len(matched) > 0, matched
}

func findingsText(findings []core.Finding) string {
	parts := make([]string, 0, len(findings)*4)
	for _, finding := range findings {
		parts = append(parts, finding.Title, finding.Category, finding.Claim, finding.Body)
	}
	return strings.Join(parts, "\n")
}

func findDefaultPackContract(packID string) (Contract, bool) {
	for _, pack := range DefaultPacks() {
		contract := pack.Contract()
		if strings.EqualFold(strings.TrimSpace(contract.ID), strings.TrimSpace(packID)) {
			return contract, true
		}
	}
	return Contract{}, false
}

func providerConfigsFromConfigForAcceptance(cfg *config.Config) (string, string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", "", nil, fmt.Errorf("configuration is required")
	}

	routes := make(map[string]llm.ProviderConfig)
	if providerKind := strings.ToLower(strings.TrimSpace(cfg.LLMProvider)); providerKind != "" {
		const quickStartDefaultRoute = "default"
		const quickStartFallbackRoute = "secondary"

		quickStart := llm.ProviderConfig{
			Kind:       providerKind,
			BaseURL:    strings.TrimSpace(cfg.LLMBaseURL),
			APIKey:     strings.TrimSpace(cfg.LLMAPIKey),
			Model:      strings.TrimSpace(cfg.LLMModel),
			MaxTokens:  4096,
			OutputMode: "tool_call",
		}
		if providerKind == llm.ProviderKindOpenAI {
			quickStart.OutputMode = "json_schema"
			quickStart.MaxCompletionTokens = 12000
			quickStart.ReasoningEffort = "medium"
		}

		defaultProvider := quickStart
		defaultProvider.RouteName = quickStartDefaultRoute
		secondaryProvider := quickStart
		secondaryProvider.RouteName = quickStartFallbackRoute
		routes[quickStartDefaultRoute] = defaultProvider
		routes[quickStartFallbackRoute] = secondaryProvider
		return quickStartDefaultRoute, quickStartFallbackRoute, routes, nil
	}

	if len(cfg.LLM.Routes) > 0 {
		defaultRoute := strings.TrimSpace(cfg.LLM.DefaultRoute)
		if defaultRoute == "" {
			return "", "", nil, fmt.Errorf("llm.default_route is required when llm.routes is configured")
		}
		for routeName, route := range cfg.LLM.Routes {
			trimmed := strings.TrimSpace(routeName)
			if trimmed == "" {
				return "", "", nil, fmt.Errorf("llm route name cannot be empty")
			}
			providerKind := strings.TrimSpace(route.Provider)
			if providerKind == "" {
				return "", "", nil, fmt.Errorf("llm.routes.%s.provider is required", trimmed)
			}
			routes[trimmed] = llm.ProviderConfig{
				Kind:                providerKind,
				BaseURL:             strings.TrimSpace(route.BaseURL),
				APIKey:              strings.TrimSpace(route.APIKey),
				Model:               strings.TrimSpace(route.Model),
				RouteName:           trimmed,
				OutputMode:          strings.TrimSpace(route.OutputMode),
				MaxTokens:           route.MaxTokens,
				MaxCompletionTokens: route.MaxCompletionTokens,
				ReasoningEffort:     strings.TrimSpace(route.ReasoningEffort),
				Temperature:         route.Temperature,
			}
		}
		return defaultRoute, strings.TrimSpace(cfg.LLM.FallbackRoute), routes, nil
	}

	const legacyDefaultRoute = "default"
	const legacyFallbackRoute = "secondary"
	legacy := llm.ProviderConfig{
		Kind:       llm.ProviderKindMiniMax,
		BaseURL:    strings.TrimSpace(cfg.AnthropicBaseURL),
		APIKey:     strings.TrimSpace(cfg.AnthropicAPIKey),
		Model:      strings.TrimSpace(cfg.AnthropicModel),
		MaxTokens:  4096,
		OutputMode: "tool_call",
	}
	defaultProvider := legacy
	defaultProvider.RouteName = legacyDefaultRoute
	secondaryProvider := legacy
	secondaryProvider.RouteName = legacyFallbackRoute
	routes[legacyDefaultRoute] = defaultProvider
	routes[legacyFallbackRoute] = secondaryProvider
	return legacyDefaultRoute, legacyFallbackRoute, routes, nil
}

func writeSyntheticAcceptanceEvidence(evidence syntheticAcceptanceEvidence) error {
	if err := os.MkdirAll(filepath.Dir(syntheticAcceptanceEvidencePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(syntheticAcceptanceEvidencePath, data, 0o644)
}

func syntheticAcceptanceCorpus() []syntheticScenario {
	return []syntheticScenario{
		{
			Name:             "security_authz_bypass",
			PackID:           "security",
			Title:            "Skip tenant ownership check on account lookup",
			Description:      "Synthetic acceptance case for authorization regression detection.",
			ExpectedKeywords: []string{"authorization", "tenant", "access control", "bypass"},
			Changes: []ctxpkg.Change{syntheticChange(
				"internal/api/accounts.go",
				`@@
- account, err := repo.GetAccountForTenant(ctx, tenantID, accountID)
+ account, err := repo.GetAccountByID(ctx, accountID)
 if err != nil {
   return nil, err
 }
`,
			)},
		},
		{
			Name:             "security_sql_injection",
			PackID:           "security",
			Title:            "Add search endpoint with raw SQL interpolation",
			Description:      "Synthetic acceptance case for injection detection.",
			ExpectedKeywords: []string{"sql", "injection", "attacker-controlled", "query"},
			Changes: []ctxpkg.Change{syntheticChange(
				"internal/store/search.go",
				`@@
- rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE email = ?", email)
+ rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE email = '"+email+"'")
 if err != nil {
   return nil, err
 }
`,
			)},
		},
		{
			Name:             "security_secret_exposure",
			PackID:           "security",
			Title:            "Return provider secret in debug response",
			Description:      "Synthetic acceptance case for secret exposure detection.",
			ExpectedKeywords: []string{"secret", "token", "credential", "exposure"},
			Changes: []ctxpkg.Change{syntheticChange(
				"internal/api/debug.go",
				`@@
+ return map[string]any{
+   "provider": cfg.Provider,
+   "api_key": cfg.APIKey,
+   "region": cfg.Region,
+ }, nil
`,
			)},
		},
		{
			Name:             "database_transaction_hole",
			PackID:           "database",
			Title:            "Split order write and inventory update across separate transactions",
			Description:      "Synthetic acceptance case for transaction hole detection.",
			ExpectedKeywords: []string{"transaction", "partial write", "consistency", "inventory"},
			Changes: []ctxpkg.Change{syntheticChange(
				"internal/orders/service.go",
				`@@
- return store.WithTx(ctx, func(tx *sql.Tx) error {
-   if err := store.InsertOrderTx(ctx, tx, order); err != nil { return err }
-   return store.DecrementInventoryTx(ctx, tx, order.SKU, order.Qty)
- })
+ if err := store.InsertOrder(ctx, order); err != nil { return err }
+ return store.DecrementInventory(ctx, order.SKU, order.Qty)
`,
			)},
		},
		{
			Name:             "database_destructive_migration",
			PackID:           "database",
			Title:            "Drop legacy column before backfill",
			Description:      "Synthetic acceptance case for destructive migration detection.",
			ExpectedKeywords: []string{"migration", "backfill", "destructive", "deploy"},
			Changes: []ctxpkg.Change{syntheticChange(
				"db/migrations/20260401_drop_status_column.sql",
				`@@
+ ALTER TABLE invoices DROP COLUMN status;
+ ALTER TABLE invoices ADD COLUMN state VARCHAR(32) NOT NULL;
`,
				"added",
			)},
		},
		{
			Name:             "database_nullability_backfill",
			PackID:           "database",
			Title:            "Make column NOT NULL without default or backfill",
			Description:      "Synthetic acceptance case for nullability/backfill risk detection.",
			ExpectedKeywords: []string{"not null", "backfill", "default", "existing rows"},
			Changes: []ctxpkg.Change{syntheticChange(
				"db/migrations/20260401_require_account_manager.sql",
				`@@
+ ALTER TABLE customers
+   ADD COLUMN account_manager_id BIGINT NOT NULL;
`,
				"added",
			)},
		},
	}
}

func syntheticChange(path, patch string, status ...string) ctxpkg.Change {
	changeStatus := "modified"
	if len(status) > 0 && strings.TrimSpace(status[0]) != "" {
		changeStatus = strings.TrimSpace(status[0])
	}
	return ctxpkg.Change{
		Path:         path,
		Status:       changeStatus,
		ChangedLines: strings.Count(patch, "\n"),
		Hunks: []ctxpkg.Hunk{{
			OldStart:     1,
			OldLines:     1,
			NewStart:     1,
			NewLines:     strings.Count(patch, "\n"),
			Patch:        patch,
			ChangedLines: strings.Count(patch, "\n"),
		}},
	}
}
