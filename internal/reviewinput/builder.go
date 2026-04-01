package reviewinput

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type RulesLoader interface {
	Load(ctx context.Context, input rules.LoadInput) (rules.LoadResult, error)
}

type Assembler interface {
	Assemble(input ctxpkg.AssembleInput) (ctxpkg.AssemblyResult, error)
}

type Builder struct {
	loader    RulesLoader
	assembler Assembler
}

type BuildRequest struct {
	Target   reviewcore.ReviewTarget
	Snapshot reviewcore.PlatformSnapshot
}

func NewBuilder(loader RulesLoader, assembler Assembler) *Builder {
	return &Builder{loader: loader, assembler: assembler}
}

func (b *Builder) Build(ctx context.Context, request BuildRequest) (reviewcore.ReviewInput, error) {
	if b == nil || b.loader == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: rules loader is required")
	}
	if b.assembler == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: assembler is required")
	}

	projectID := parseProjectID(request.Snapshot.Metadata)
	loadResult, err := b.loader.Load(ctx, rules.LoadInput{
		ProjectID: projectID,
		HeadSHA:   request.Snapshot.HeadSHA,
	})
	if err != nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: load rules: %w", err)
	}

	assemblyResult, err := b.assembler.Assemble(ctxpkg.AssembleInput{
		Project: ctxpkg.ProjectContext{
			ProjectID: projectID,
			FullPath:  request.Target.Repository,
		},
		MergeRequest: ctxpkg.MergeRequestContext{
			IID:         request.Target.Number,
			Title:       request.Snapshot.Title,
			Description: request.Snapshot.Description,
		},
		Version: ctxpkg.VersionContext{
			BaseSHA:    request.Snapshot.BaseSHA,
			StartSHA:   request.Snapshot.StartSHA,
			HeadSHA:    request.Snapshot.HeadSHA,
			PatchIDSHA: valueOrEmpty(request.Snapshot.Metadata, "patch_id_sha"),
		},
		Rules:    loadResult.Trusted,
		Settings: settingsFromEffectivePolicy(loadResult.EffectivePolicy),
		Diffs:    gitlabDiffsFromSnapshot(request.Snapshot),
	})
	if err != nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: assemble context: %w", err)
	}

	contextText, err := marshalContextText(loadResult.SystemPrompt, assemblyResult.Request)
	if err != nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: encode context: %w", err)
	}
	requestPayload, err := json.Marshal(assemblyResult.Request)
	if err != nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("review input builder: encode request payload: %w", err)
	}

	metadata := cloneMetadata(request.Snapshot.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if loadResult.EffectivePolicy.ProviderRoute != "" {
		metadata["provider_route"] = loadResult.EffectivePolicy.ProviderRoute
	}
	if loadResult.EffectivePolicy.OutputLanguage != "" {
		metadata["output_language"] = loadResult.EffectivePolicy.OutputLanguage
	}
	if loadResult.Trusted.RulesDigest != "" {
		metadata["rules_digest"] = loadResult.Trusted.RulesDigest
	}

	policy := map[string]any{
		"provider_route":       loadResult.EffectivePolicy.ProviderRoute,
		"output_language":      loadResult.EffectivePolicy.OutputLanguage,
		"confidence_threshold": loadResult.EffectivePolicy.ConfidenceThreshold,
		"severity_threshold":   loadResult.EffectivePolicy.SeverityThreshold,
		"gate_mode":            loadResult.EffectivePolicy.GateMode,
	}

	return reviewcore.ReviewInput{
		Target:         request.Target,
		Snapshot:       request.Snapshot,
		Metadata:       metadata,
		Policy:         policy,
		SystemPrompt:   loadResult.SystemPrompt,
		RequestPayload: requestPayload,
		ContextText:    contextText,
		Sections: buildSections(sectionInputs{
			target:         request.Target,
			snapshot:       request.Snapshot,
			metadata:       metadata,
			policy:         policy,
			systemPrompt:   loadResult.SystemPrompt,
			requestPayload: requestPayload,
			contextText:    contextText,
		}),
	}, nil
}

type sectionInputs struct {
	target         reviewcore.ReviewTarget
	snapshot       reviewcore.PlatformSnapshot
	metadata       map[string]string
	policy         map[string]any
	systemPrompt   string
	requestPayload []byte
	contextText    string
}

func buildSections(input sectionInputs) []reviewcore.ReviewInputSection {
	sections := []reviewcore.ReviewInputSection{
		section("target", "platform_target", mustJSONText(map[string]any{
			"platform":   input.target.Platform,
			"repository": input.target.Repository,
			"number":     input.target.Number,
			"url":        input.target.URL,
			"head_sha":   input.snapshot.HeadSHA,
			"base_sha":   input.snapshot.BaseSHA,
			"start_sha":  input.snapshot.StartSHA,
		}), true),
		section("policy", "review_policy", mustJSONText(input.policy), false),
		section("system_prompt", "system_prompt", input.systemPrompt, false),
		section("request_payload", "assembled_request", string(input.requestPayload), true),
		section("platform_metadata", "platform_metadata", mustJSONText(input.metadata), true),
		section("assembled_context", "assembled_context", input.contextText, true),
	}

	result := make([]reviewcore.ReviewInputSection, 0, len(sections))
	for _, section := range sections {
		if section.Content == "" || section.Content == "null" || section.Content == "{}" {
			continue
		}
		result = append(result, section)
	}
	return result
}

func section(id, kind, content string, volatile bool) reviewcore.ReviewInputSection {
	return reviewcore.ReviewInputSection{
		ID:       id,
		Kind:     kind,
		Content:  content,
		Volatile: volatile,
		CacheKey: sectionCacheKey(id, content, volatile),
	}
}

func sectionCacheKey(id, content string, volatile bool) string {
	sum := sha256.Sum256([]byte(id + "\x00" + content))
	prefix := "stable"
	if volatile {
		prefix = "volatile"
	}
	return fmt.Sprintf("%s:%x", prefix, sum[:8])
}

func mustJSONText(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func settingsFromEffectivePolicy(policy rules.EffectivePolicy) ctxpkg.PolicySettings {
	settings := ctxpkg.DefaultPolicySettings()
	settings.IncludePaths = append([]string(nil), policy.IncludePaths...)
	settings.ExcludePaths = append([]string(nil), policy.ExcludePaths...)
	if policy.ContextLinesBefore > 0 {
		settings.ContextLinesBefore = policy.ContextLinesBefore
	}
	if policy.ContextLinesAfter > 0 {
		settings.ContextLinesAfter = policy.ContextLinesAfter
	}
	if policy.MaxChangedLines > 0 {
		settings.MaxChangedLines = policy.MaxChangedLines
	}
	if policy.MaxFiles > 0 {
		settings.MaxFiles = policy.MaxFiles
	}
	return settings
}

func marshalContextText(systemPrompt string, request ctxpkg.ReviewRequest) (string, error) {
	payload := struct {
		SystemPrompt string               `json:"system_prompt,omitempty"`
		Request      ctxpkg.ReviewRequest `json:"request"`
	}{
		SystemPrompt: systemPrompt,
		Request:      request,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseProjectID(metadata map[string]string) int64 {
	if metadata == nil {
		return 0
	}
	raw := metadata["project_id"]
	if raw == "" {
		return 0
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return projectID
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func valueOrEmpty(metadata map[string]string, key string) string {
	if metadata == nil {
		return ""
	}
	return metadata[key]
}

func gitlabDiffsFromSnapshot(snapshot reviewcore.PlatformSnapshot) []legacygitlab.MergeRequestDiff {
	switch raw := snapshot.Opaque.(type) {
	case legacygitlab.MergeRequestSnapshot:
		return append([]legacygitlab.MergeRequestDiff(nil), raw.Diffs...)
	case *legacygitlab.MergeRequestSnapshot:
		if raw == nil {
			return nil
		}
		return append([]legacygitlab.MergeRequestDiff(nil), raw.Diffs...)
	case interface {
		ReviewDiffs() []legacygitlab.MergeRequestDiff
	}:
		return raw.ReviewDiffs()
	default:
		return nil
	}
}
