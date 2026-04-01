package reviewinput

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type RulesLoader interface {
	Load(ctx context.Context, input rules.LoadInput) (rules.LoadResult, error)
}

type BuildInput struct {
	Snapshot             core.PlatformSnapshot
	ProjectDefaultBranch string
	ProjectPolicy        *db.ProjectPolicy
	MergeRequestID       int64
}

type Builder struct {
	rulesLoader RulesLoader
	assembler   *ctxpkg.Assembler
	store       ctxpkg.HistoricalStore
}

func NewBuilder(rulesLoader RulesLoader, assembler *ctxpkg.Assembler, store ctxpkg.HistoricalStore) *Builder {
	if assembler == nil {
		assembler = ctxpkg.NewAssembler()
	}
	return &Builder{
		rulesLoader: rulesLoader,
		assembler:   assembler,
		store:       store,
	}
}

func (b *Builder) Build(ctx context.Context, input BuildInput) (core.ReviewInput, error) {
	if b == nil || b.rulesLoader == nil || b.assembler == nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: builder dependencies are not configured")
	}

	changedPaths := make([]string, 0, len(input.Snapshot.Diffs))
	for _, diff := range input.Snapshot.Diffs {
		if diff.NewPath != "" {
			changedPaths = append(changedPaths, diff.NewPath)
			continue
		}
		if diff.OldPath != "" {
			changedPaths = append(changedPaths, diff.OldPath)
		}
	}

	loadResult, err := b.rulesLoader.Load(ctx, rules.LoadInput{
		ProjectID:              input.Snapshot.Change.ProjectID,
		RepositoryRef:          input.Snapshot.Target.Repository,
		HeadSHA:                input.Snapshot.Version.HeadSHA,
		ProjectPolicy:          input.ProjectPolicy,
		ChangedPaths:           changedPaths,
		InstructionConfigPaths: instructionConfigPaths(input.Snapshot.Target.Platform),
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: load rules: %w", err)
	}

	settings, err := ctxpkg.SettingsFromPolicy(input.ProjectPolicy)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: policy settings: %w", err)
	}

	historical, err := ctxpkg.LoadHistoricalContext(ctx, b.store, input.MergeRequestID)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: historical context: %w", err)
	}

	assembled, err := b.assembler.Assemble(ctxpkg.AssembleInput{
		ReviewRunID: 0,
		Project: ctxpkg.ProjectContext{
			ProjectID:     input.Snapshot.Change.ProjectID,
			FullPath:      input.Snapshot.Target.Repository,
			DefaultBranch: input.ProjectDefaultBranch,
		},
		MergeRequest: ctxpkg.MergeRequestContext{
			IID:         input.Snapshot.Change.Number,
			Title:       input.Snapshot.Change.Title,
			Description: input.Snapshot.Change.Description,
			Author:      input.Snapshot.Change.Author.Username,
		},
		Version: ctxpkg.VersionContext{
			BaseSHA:    input.Snapshot.Version.BaseSHA,
			StartSHA:   input.Snapshot.Version.StartSHA,
			HeadSHA:    input.Snapshot.Version.HeadSHA,
			PatchIDSHA: input.Snapshot.Version.PatchIDSHA,
		},
		Rules:             loadResult.Trusted,
		Settings:          settings,
		Diffs:             legacyDiffsFromSnapshot(input.Snapshot.Diffs),
		HistoricalContext: historical,
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: assemble request: %w", err)
	}

	requestPayload, err := json.Marshal(assembled.Request)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: encode request payload: %w", err)
	}

	metadata := snapshotMetadata(input.Snapshot)
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

	contextText, err := marshalContextText(loadResult.SystemPrompt, assembled.Request)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: encode context: %w", err)
	}

	return core.ReviewInput{
		Target:          input.Snapshot.Target,
		Snapshot:        input.Snapshot,
		Request:         assembled.Request,
		EffectivePolicy: loadResult.EffectivePolicy,
		Warnings:        loadResult.Warnings,
		Metadata:        metadata,
		Policy:          policy,
		SystemPrompt:    loadResult.SystemPrompt,
		RequestPayload:  requestPayload,
		ContextText:     contextText,
		Sections: buildSections(sectionInputs{
			target:         input.Snapshot.Target,
			snapshot:       input.Snapshot,
			metadata:       metadata,
			policy:         policy,
			systemPrompt:   loadResult.SystemPrompt,
			requestPayload: requestPayload,
			contextText:    contextText,
		}),
	}, nil
}

type sectionInputs struct {
	target         core.ReviewTarget
	snapshot       core.PlatformSnapshot
	metadata       map[string]string
	policy         map[string]any
	systemPrompt   string
	requestPayload []byte
	contextText    string
}

func buildSections(input sectionInputs) []core.ReviewInputSection {
	sections := []core.ReviewInputSection{
		section("target", "platform_target", mustJSONText(map[string]any{
			"platform":   input.target.Platform,
			"repository": input.target.Repository,
			"number":     input.target.ChangeNumber,
			"url":        input.target.URL,
			"head_sha":   input.snapshot.Version.HeadSHA,
			"base_sha":   input.snapshot.Version.BaseSHA,
			"start_sha":  input.snapshot.Version.StartSHA,
		}), true),
		section("policy", "review_policy", mustJSONText(input.policy), false),
		section("system_prompt", "system_prompt", input.systemPrompt, false),
		section("request_payload", "assembled_request", string(input.requestPayload), true),
		section("platform_metadata", "platform_metadata", mustJSONText(input.metadata), true),
		section("assembled_context", "assembled_context", input.contextText, true),
	}

	result := make([]core.ReviewInputSection, 0, len(sections))
	for _, item := range sections {
		if item.Content == "" || item.Content == "null" || item.Content == "{}" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func section(id, kind, content string, volatile bool) core.ReviewInputSection {
	return core.ReviewInputSection{
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

func snapshotMetadata(snapshot core.PlatformSnapshot) map[string]string {
	metadata := map[string]string{}
	if snapshot.Change.ProjectID != 0 {
		metadata["project_id"] = strconv.FormatInt(snapshot.Change.ProjectID, 10)
	}
	if snapshot.Version.PatchIDSHA != "" {
		metadata["patch_id_sha"] = snapshot.Version.PatchIDSHA
	}
	if snapshot.Change.PlatformID != "" {
		metadata["platform_change_id"] = snapshot.Change.PlatformID
	}
	if snapshot.Version.PlatformVersionID != "" {
		metadata["platform_version_id"] = snapshot.Version.PlatformVersionID
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func legacyDiffsFromSnapshot(diffs []core.PlatformDiff) []legacygitlab.MergeRequestDiff {
	if len(diffs) == 0 {
		return nil
	}
	legacyDiffs := make([]legacygitlab.MergeRequestDiff, 0, len(diffs))
	for _, diff := range diffs {
		legacyDiffs = append(legacyDiffs, legacygitlab.MergeRequestDiff{
			OldPath:       diff.OldPath,
			NewPath:       diff.NewPath,
			Diff:          diff.Diff,
			AMode:         diff.AMode,
			BMode:         diff.BMode,
			NewFile:       diff.NewFile,
			RenamedFile:   diff.RenamedFile,
			DeletedFile:   diff.DeletedFile,
			GeneratedFile: diff.GeneratedFile,
			Collapsed:     diff.Collapsed,
			TooLarge:      diff.TooLarge,
		})
	}
	return legacyDiffs
}

func instructionConfigPaths(platform core.Platform) []string {
	switch platform {
	case core.PlatformGitHub:
		return []string{".github/ai-review.yaml"}
	default:
		return []string{".gitlab/ai-review.yaml"}
	}
}
