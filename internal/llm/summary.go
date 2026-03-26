package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/reviewlang"
)

func persistSummaryNoteFallback(ctx context.Context, store ProcessorStore, run db.ReviewRun, result ReviewResult) error {
	if result.SummaryNote == nil || strings.TrimSpace(result.SummaryNote.BodyMarkdown) == "" {
		return nil
	}
	_, err := store.InsertCommentAction(ctx, db.InsertCommentActionParams{ReviewRunID: run.ID, ReviewFindingID: sql.NullInt64{}, ActionType: "summary_note", IdempotencyKey: fmt.Sprintf("run:%d:parser_error_summary_note", run.ID), Status: "pending"})
	if err != nil && !db.IsDuplicateKeyError(err) {
		return err
	}
	return nil
}

func buildDegradationSummaryNote(run db.ReviewRun, assembled ctxpkg.AssemblyResult, language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		parts := []string{
			fmt.Sprintf("AI Review 摘要（run %d）", run.ID),
			"",
			"本次变更较大，已启用降级审查模式。",
			assembled.Coverage.Summary,
		}
		if len(assembled.Coverage.ReviewedPaths) > 0 {
			parts = append(parts, "", "已审查的高优先级文件：", "- "+strings.Join(assembled.Coverage.ReviewedPaths, "\n- "))
		}
		skipped := make([]string, 0, len(assembled.Excluded))
		for _, file := range assembled.Excluded {
			if file.Reason != ctxpkg.ExcludedReasonScopeLimit {
				continue
			}
			skipped = append(skipped, fmt.Sprintf("- %s (%s)", file.Path, file.Reason))
		}
		if len(skipped) > 0 {
			parts = append(parts, "", "已跳过的文件：", strings.Join(skipped, "\n"))
		}
		return strings.Join(parts, "\n")
	}
	parts := []string{
		fmt.Sprintf("AI review summary for run %d", run.ID),
		"",
		"Large merge request degradation mode was activated.",
		assembled.Coverage.Summary,
	}
	if len(assembled.Coverage.ReviewedPaths) > 0 {
		parts = append(parts, "", "Reviewed highest-priority files:", "- "+strings.Join(assembled.Coverage.ReviewedPaths, "\n- "))
	}
	skipped := make([]string, 0, len(assembled.Excluded))
	for _, file := range assembled.Excluded {
		if file.Reason != ctxpkg.ExcludedReasonScopeLimit {
			continue
		}
		skipped = append(skipped, fmt.Sprintf("- %s (%s)", file.Path, file.Reason))
	}
	if len(skipped) > 0 {
		parts = append(parts, "", "Skipped files:", strings.Join(skipped, "\n"))
	}
	return strings.Join(parts, "\n")
}

func parserErrorSummary(language, reviewRunID string) string {
	if reviewlang.IsChinese(language) {
		return fmt.Sprintf("本次 review run %s 的模型输出无法解析，未生成可用的审查结果。", reviewRunID)
	}
	return "Provider response could not be parsed into a review result."
}

func parserErrorSummaryNote(language, reviewRunID string) string {
	if reviewlang.IsChinese(language) {
		return fmt.Sprintf("Review run %s 的模型输出无法解析，原始返回已被拒绝，因此没有创建任何行内评论。", reviewRunID)
	}
	return fmt.Sprintf("Review run %s could not parse the provider response. The raw provider output was rejected and no inline findings were created.", reviewRunID)
}

func buildSummarySystemPrompt(language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		return `你是一名合并请求摘要撰写助手。你的任务是用简体中文（zh-CN）产出清晰、简洁的变更 walkthrough，指出高风险区域，并给出结论。

输出格式要求：
1. Return ONLY valid JSON.
2. Do not wrap the JSON in markdown fences.
3. Do not add any prose before or after the JSON object.
4. Required top-level fields: schema_version, review_run_id, walkthrough, verdict.

硬性约束：
1. walkthrough 需要解释“改了什么”和“为什么改”，不要逐行复述 diff。
2. risk_areas 只保留最容易引入 bug 或回归的文件或模块。
3. verdict 只能是：approve（未发现问题）、request_changes（存在阻塞问题）、comment（只有非阻塞观察）。
4. 如果有无法完全验证的区域，写入 blind_spots。
5. 不要重复 review findings；summary 是独立、互补的视角。`
	}
	return fmt.Sprintf(`You are a merge request summary writer. Your job is to produce a clear, concise walkthrough of what this merge request changes, identify risk areas, and give a verdict. All narrative text must be written in %s.

Output format requirements:
1. Return ONLY valid JSON.
2. Do not wrap the JSON in markdown fences.
3. Do not add any prose before or after the JSON object.
4. Required top-level fields: schema_version, review_run_id, walkthrough, verdict.

Hard constraints:
1. The walkthrough should explain WHAT changed and WHY at a high level. Do not list every line change.
2. Risk areas should highlight files/modules where the changes have the highest potential for bugs or regressions.
3. The verdict must be one of: approve (no issues found), request_changes (blocking issues exist), comment (non-blocking observations only).
4. If you cannot fully verify certain areas, list them in blind_spots.
5. Do NOT duplicate the findings from the review chain. The summary is a separate, complementary view.`, language)
}

func (p *MiniMaxProvider) SummaryRequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return p.SummaryRequestPayloadWithSystemPrompt(request, buildSummarySystemPrompt(reviewlang.DefaultOutputLanguage))
}

func (p *MiniMaxProvider) SummaryRequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{
		"model":       p.model,
		"max_tokens":  p.maxTokens,
		"temperature": p.temperature,
		"system":      systemPrompt,
		"messages":    []map[string]any{{"role": "user", "content": mustJSON(request)}},
	}
}

func (p *MiniMaxProvider) Summarize(ctx context.Context, request ctxpkg.ReviewRequest) (SummaryResponse, error) {
	return p.SummarizeWithSystemPrompt(ctx, request, buildSummarySystemPrompt(reviewlang.DefaultOutputLanguage))
}

func (p *MiniMaxProvider) SummarizeWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (SummaryResponse, error) {
	if p.rateLimiter != nil {
		if err := p.rateLimiter.Wait(ctx, strings.TrimSpace(p.routeName)); err != nil {
			return SummaryResponse{}, err
		}
	}
	started := p.now()
	message, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:       anthropic.Model(p.model),
		MaxTokens:   p.maxTokens,
		Temperature: anthropic.Float(p.temperature),
		System:      []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:    []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(mustJSON(request)))},
	})
	if err != nil {
		return SummaryResponse{}, err
	}
	text := collectMessageText(message)
	var result SummaryResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return SummaryResponse{}, fmt.Errorf("llm: parse summary response: %w", err)
	}
	return SummaryResponse{
		Result:  result,
		RawText: text,
		Latency: p.now().Sub(started),
		Tokens:  int64(message.Usage.InputTokens + message.Usage.OutputTokens),
		Model:   p.routeName,
	}, nil
}

func renderSummaryFromWalkthrough(summary SummaryResult, language string) string {
	language = reviewlang.Normalize(language)
	if reviewlang.IsChinese(language) {
		var parts []string
		parts = append(parts, "## 变更解读\n")
		parts = append(parts, summary.Walkthrough)
		if len(summary.RiskAreas) > 0 {
			parts = append(parts, "\n### 风险区域\n")
			for _, area := range summary.RiskAreas {
				parts = append(parts, fmt.Sprintf("- **%s**（%s）：%s", area.Path, area.Severity, area.Description))
			}
		}
		if len(summary.BlindSpots) > 0 {
			parts = append(parts, "\n### 盲区\n")
			for _, spot := range summary.BlindSpots {
				parts = append(parts, "- "+spot)
			}
		}
		parts = append(parts, fmt.Sprintf("\n**结论**：%s", summary.Verdict))
		return strings.Join(parts, "\n")
	}
	var parts []string
	parts = append(parts, "## MR Walkthrough\n")
	parts = append(parts, summary.Walkthrough)
	if len(summary.RiskAreas) > 0 {
		parts = append(parts, "\n### Risk Areas\n")
		for _, area := range summary.RiskAreas {
			parts = append(parts, fmt.Sprintf("- **%s** (%s): %s", area.Path, area.Severity, area.Description))
		}
	}
	if len(summary.BlindSpots) > 0 {
		parts = append(parts, "\n### Blind Spots\n")
		for _, spot := range summary.BlindSpots {
			parts = append(parts, "- "+spot)
		}
	}
	parts = append(parts, fmt.Sprintf("\n**Verdict**: %s", summary.Verdict))
	return strings.Join(parts, "\n")
}
