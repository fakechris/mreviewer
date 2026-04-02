package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type anthropicToolProfile struct {
	kind       string
	outputMode string
}

var (
	miniMaxProfile   = anthropicToolProfile{kind: ProviderKindMiniMax, outputMode: openAIOutputModeToolCall}
	anthropicProfile = anthropicToolProfile{kind: ProviderKindAnthropic, outputMode: openAIOutputModeToolCall}
)

type MiniMaxProvider struct {
	client         anthropic.Client
	model          string
	maxTokens      int64
	temperature    float64
	systemPrompt   string
	routeName      string
	profile        anthropicToolProfile
	rateLimiter    RateLimiter
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	timeoutRetries int
}

func NewMiniMaxProvider(cfg ProviderConfig) (*MiniMaxProvider, error) {
	return newAnthropicToolProvider(cfg, miniMaxProfile)
}

func NewAnthropicProvider(cfg ProviderConfig) (*MiniMaxProvider, error) {
	return newAnthropicToolProvider(cfg, anthropicProfile)
}

func newAnthropicToolProvider(cfg ProviderConfig, profile anthropicToolProfile) (*MiniMaxProvider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("llm: base URL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm: model is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if cfg.TimeoutRetries <= 0 {
		cfg.TimeoutRetries = defaultTimeoutRetry
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = defaultTemperature
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	outputMode := strings.ToLower(strings.TrimSpace(cfg.OutputMode))
	if outputMode == "" {
		outputMode = profile.outputMode
	}
	if outputMode != openAIOutputModeToolCall {
		return nil, fmt.Errorf("llm: provider %q only supports output_mode %q", profile.kind, openAIOutputModeToolCall)
	}
	options := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.HTTPClient != nil {
		options = append(options, option.WithHTTPClient(cfg.HTTPClient))
	}
	client := anthropic.NewClient(options...)
	routeName := strings.TrimSpace(cfg.RouteName)
	if routeName == "" {
		routeName = strings.TrimSpace(cfg.Model)
	}
	return &MiniMaxProvider{
		client:         client,
		model:          cfg.Model,
		maxTokens:      cfg.MaxTokens,
		temperature:    cfg.Temperature,
		systemPrompt:   cfg.SystemPrompt,
		routeName:      routeName,
		profile:        profile,
		rateLimiter:    cfg.RateLimiter,
		now:            cfg.Now,
		sleep:          cfg.Sleep,
		timeoutRetries: cfg.TimeoutRetries,
	}, nil
}

func (p *MiniMaxProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return p.RequestPayloadWithSystemPrompt(request, p.systemPrompt)
}

func (p *MiniMaxProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{
		"model":       p.model,
		"max_tokens":  p.maxTokens,
		"temperature": p.temperature,
		"system":      systemPrompt,
		"messages":    []map[string]any{{"role": "user", "content": mustJSON(request)}},
		"tools":       []map[string]any{reviewToolPayloadForProfile(p.profile)},
		"tool_choice": map[string]any{"type": "tool", "name": reviewSubmitToolName},
	}
}

func (p *MiniMaxProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	return p.ReviewWithSystemPrompt(ctx, request, p.systemPrompt)
}

func (p *MiniMaxProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p.rateLimiter != nil {
		if err := p.rateLimiter.Wait(ctx, strings.TrimSpace(p.routeName)); err != nil {
			return ProviderResponse{}, err
		}
	}
	var lastErr error
	maxAttempts := p.timeoutRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		raw, tokens, latency, err := p.callReviewTool(ctx, systemPrompt, mustJSON(request))
		if err != nil {
			lastErr = err
			var structuredMiss *structuredOutputMissError
			if errors.As(err, &structuredMiss) {
				if strings.TrimSpace(structuredMiss.rawResponse) != "" {
					repairedRaw, repairTokens, repairLatency, repairErr := p.callReviewTool(
						ctx,
						systemPrompt,
						buildReviewRepairPayload(request, structuredMiss.rawResponse, structuredMiss.cause),
					)
					latency += repairLatency
					tokens += repairTokens
					if repairErr != nil {
						return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
							cause:       repairErr,
							rawResponse: structuredMiss.rawResponse,
							latency:     latency,
							tokens:      tokens,
							model:       p.routeName,
						})
					}
					if repairValidationErr := validateReviewResultStrictJSON(repairedRaw); repairValidationErr != nil {
						result, salvageErr := salvageReviewResultAfterStrictValidationFailure(repairedRaw, repairValidationErr)
						if salvageErr != nil {
							return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
								cause:       salvageErr,
								rawResponse: repairedRaw,
								latency:     latency,
								tokens:      tokens,
								model:       p.routeName,
							})
						}
						return ProviderResponse{
							Result:          result,
							RawText:         repairedRaw,
							Latency:         latency,
							Tokens:          tokens,
							FallbackStage:   "repair_retry",
							Model:           p.routeName,
							ResponsePayload: map[string]any{"text": repairedRaw, "fallback_stage": "repair_retry"},
						}, nil
					}
					result, parseStage, parseErr := ParseReviewResult(repairedRaw)
					if parseErr != nil {
						return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
							cause:       parseErr,
							rawResponse: repairedRaw,
							latency:     latency,
							tokens:      tokens,
							model:       p.routeName,
						})
					}
					stage := fallbackStageWithParseStage("repair_retry", parseStage)
					return ProviderResponse{
						Result:          result,
						RawText:         repairedRaw,
						Latency:         latency,
						Tokens:          tokens,
						FallbackStage:   stage,
						Model:           p.routeName,
						ResponsePayload: map[string]any{"text": repairedRaw, "fallback_stage": stage},
					}, nil
				}
				return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
					cause:       structuredMiss.cause,
					rawResponse: structuredMiss.rawResponse,
					latency:     latency,
					tokens:      tokens,
					model:       p.routeName,
				})
			}
			if !isTimeoutError(err) || attempt == maxAttempts-1 {
				return ProviderResponse{}, err
			}
			if sleepErr := p.sleep(ctx, time.Duration(attempt+1)*50*time.Millisecond); sleepErr != nil {
				return ProviderResponse{}, sleepErr
			}
			continue
		}
		stage := "direct"
		if validationErr := validateReviewResultStrictJSON(raw); validationErr != nil {
			repairedRaw, repairTokens, repairLatency, repairErr := p.callReviewTool(
				ctx,
				systemPrompt,
				buildReviewRepairPayload(request, raw, validationErr),
			)
			latency += repairLatency
			tokens += repairTokens
			if repairErr != nil {
				return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
					cause:       repairErr,
					rawResponse: raw,
					latency:     latency,
					tokens:      tokens,
					model:       p.routeName,
				})
			}
			if repairValidationErr := validateReviewResultStrictJSON(repairedRaw); repairValidationErr != nil {
				result, salvageErr := salvageReviewResultAfterStrictValidationFailure(repairedRaw, repairValidationErr)
				if salvageErr != nil {
					return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
						cause:       salvageErr,
						rawResponse: repairedRaw,
						latency:     latency,
						tokens:      tokens,
						model:       p.routeName,
					})
				}
				return ProviderResponse{
					Result:          result,
					RawText:         repairedRaw,
					Latency:         latency,
					Tokens:          tokens,
					FallbackStage:   "repair_retry",
					Model:           p.routeName,
					ResponsePayload: map[string]any{"text": repairedRaw, "fallback_stage": "repair_retry"},
				}, nil
			}
			raw = repairedRaw
			stage = "repair_retry"
		}
		result, parseStage, parseErr := ParseReviewResult(raw)
		if parseErr != nil {
			return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
				cause:       parseErr,
				rawResponse: raw,
				latency:     latency,
				tokens:      tokens,
				model:       p.routeName,
			})
		}
		if stage == "direct" {
			stage = parseStage
		} else {
			stage = fallbackStageWithParseStage(stage, parseStage)
		}
		return ProviderResponse{Result: result, RawText: raw, Latency: latency, Tokens: tokens, FallbackStage: stage, Model: p.routeName, ResponsePayload: map[string]any{"text": raw, "fallback_stage": stage}}, nil
	}
	return ProviderResponse{}, lastErr
}

func fallbackStageWithParseStage(base, parseStage string) string {
	parseStage = strings.TrimSpace(parseStage)
	if parseStage == "" || parseStage == "direct" {
		return base
	}
	return base + "_" + parseStage
}

func (p *MiniMaxProvider) callReviewTool(ctx context.Context, systemPrompt string, userContent string) (string, int64, time.Duration, error) {
	started := p.now()
	message, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:       anthropic.Model(p.model),
		MaxTokens:   p.maxTokens,
		Temperature: anthropic.Float(p.temperature),
		System:      []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:    []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(userContent))},
		Tools:       []anthropic.ToolUnionParam{reviewToolParamForProfile(p.profile)},
		ToolChoice:  anthropic.ToolChoiceParamOfTool(reviewSubmitToolName),
	})
	if err != nil {
		return "", 0, p.now().Sub(started), err
	}
	totalTokens := int64(message.Usage.InputTokens + message.Usage.OutputTokens)
	text, err := collectToolUseInput(message, reviewSubmitToolName)
	if err != nil {
		return "", totalTokens, p.now().Sub(started), &structuredOutputMissError{
			cause:       err,
			rawResponse: collectMessageText(message),
		}
	}
	return text, totalTokens, p.now().Sub(started), nil
}

const reviewSubmitToolName = "submit_review"

func reviewToolParam() anthropic.ToolUnionParam {
	return reviewToolParamWithSchema(reviewResultSchema())
}

func reviewResultSchemaForProfile(profile anthropicToolProfile) map[string]any {
	if profile.kind == ProviderKindAnthropic {
		return reviewResultSchemaAnthropicCompact()
	}
	return reviewResultSchema()
}

func reviewToolParamForProfile(profile anthropicToolProfile) anthropic.ToolUnionParam {
	return reviewToolParamWithSchema(reviewResultSchemaForProfile(profile))
}

func reviewToolParamWithSchema(schema map[string]any) anthropic.ToolUnionParam {
	properties, _ := schema["properties"]
	required, _ := schema["required"].([]string)
	var tool anthropic.ToolParam
	tool.Name = reviewSubmitToolName
	tool.Description = anthropic.String("Emit the final merge request review result as structured JSON.")
	tool.Strict = anthropic.Bool(true)
	tool.InputSchema = anthropic.ToolInputSchemaParam{
		Properties: properties,
		Required:   required,
		ExtraFields: map[string]any{
			"additionalProperties": false,
		},
	}
	return anthropic.ToolUnionParam{OfTool: &tool}
}

func reviewToolPayload() map[string]any {
	return reviewToolPayloadWithSchema(reviewResultSchema())
}

func reviewToolPayloadForProfile(profile anthropicToolProfile) map[string]any {
	return reviewToolPayloadWithSchema(reviewResultSchemaForProfile(profile))
}

func reviewToolPayloadWithSchema(schema map[string]any) map[string]any {
	return map[string]any{
		"name":         reviewSubmitToolName,
		"description":  "Emit the final merge request review result as structured JSON.",
		"strict":       true,
		"input_schema": schema,
	}
}
