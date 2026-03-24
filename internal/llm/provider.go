package llm

import (
	"context"
	"net/http"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

const (
	defaultSystemPrompt             = "You are an expert GitLab merge request reviewer. Return only valid JSON matching the provided schema."
	defaultMaxTokens          int64 = 4096
	defaultTemperature              = 0.2
	defaultTimeoutRetry             = 3
	parserErrorCode                 = "parser_error"
	providerTimeoutCode             = "provider_timeout"
	providerRequestFailedCode       = "provider_request_failed"
)

type Provider interface {
	Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error)
	RequestPayload(request ctxpkg.ReviewRequest) map[string]any
}

type DynamicPromptProvider interface {
	Provider
	ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error)
	RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any
}

type SummaryProvider interface {
	Summarize(ctx context.Context, request ctxpkg.ReviewRequest) (SummaryResponse, error)
}

type DynamicSummaryProvider interface {
	SummaryProvider
	SummarizeWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (SummaryResponse, error)
}

type SummaryResponse struct {
	Result  SummaryResult
	RawText string
	Latency time.Duration
	Tokens  int64
	Model   string
}

type RateLimiter interface {
	Wait(context.Context, string) error
}

type ProviderResponse struct {
	Result          ReviewResult
	RawText         string
	Latency         time.Duration
	Tokens          int64
	FallbackStage   string
	Model           string
	ResponsePayload map[string]any
}

type ReviewResult struct {
	SchemaVersion string          `json:"schema_version"`
	ReviewRunID   string          `json:"review_run_id"`
	Summary       string          `json:"summary"`
	Findings      []ReviewFinding `json:"findings"`
	Status        string          `json:"status,omitempty"`
	SummaryNote   *SummaryNote    `json:"summary_note,omitempty"`
	BlindSpots    []string        `json:"blind_spots,omitempty"`
}

type SummaryNote struct {
	BodyMarkdown string `json:"body_markdown"`
}

type SummaryResult struct {
	SchemaVersion string     `json:"schema_version"`
	ReviewRunID   string     `json:"review_run_id"`
	Walkthrough   string     `json:"walkthrough"`
	RiskAreas     []RiskArea `json:"risk_areas,omitempty"`
	BlindSpots    []string   `json:"blind_spots,omitempty"`
	Verdict       string     `json:"verdict"`
}

type RiskArea struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

type ReviewFinding struct {
	Category               string   `json:"category"`
	Severity               string   `json:"severity"`
	Confidence             float64  `json:"confidence"`
	Title                  string   `json:"title"`
	BodyMarkdown           string   `json:"body_markdown"`
	Path                   string   `json:"path"`
	AnchorKind             string   `json:"anchor_kind"`
	OldLine                *int32   `json:"old_line,omitempty"`
	NewLine                *int32   `json:"new_line,omitempty"`
	RangeStartKind         string   `json:"range_start_kind,omitempty"`
	RangeStartOldLine      *int32   `json:"range_start_old_line,omitempty"`
	RangeStartNewLine      *int32   `json:"range_start_new_line,omitempty"`
	RangeEndKind           string   `json:"range_end_kind,omitempty"`
	RangeEndOldLine        *int32   `json:"range_end_old_line,omitempty"`
	RangeEndNewLine        *int32   `json:"range_end_new_line,omitempty"`
	AnchorSnippet          string   `json:"anchor_snippet,omitempty"`
	Evidence               []string `json:"evidence,omitempty"`
	SuggestedPatch         string   `json:"suggested_patch,omitempty"`
	CanonicalKey           string   `json:"canonical_key,omitempty"`
	Symbol                 string   `json:"symbol,omitempty"`
	TriggerCondition       string   `json:"trigger_condition,omitempty"`
	Impact                 string   `json:"impact,omitempty"`
	IntroducedByThisChange bool     `json:"introduced_by_this_change"`
	BlindSpots             []string `json:"blind_spots,omitempty"`
	NoFindingReason        string   `json:"no_finding_reason,omitempty"`
}

type ProviderConfig struct {
	Kind                string
	BaseURL             string
	APIKey              string
	Model               string
	MaxTokens           int64
	MaxCompletionTokens int64
	SystemPrompt        string
	RouteName           string
	OutputMode          string
	ReasoningEffort     string
	TimeoutRetries      int
	Temperature         float64
	HTTPClient          *http.Client
	RateLimiter         RateLimiter
	Now                 func() time.Time
	Sleep               func(context.Context, time.Duration) error
}
