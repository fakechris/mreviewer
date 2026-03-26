# TODOS

## LLM / Consensus

### Unify Anthropic compact schema for consensus matching

**What:** Anthropic compact schema (`reviewResultSchemaAnthropicCompact()`) strips `canonical_key`, `symbol`, and line number fields that consensus matching depends on.

**Why:** Without these fields, Anthropic provider responses cannot participate in cross-model consensus matching. The P2 multi-model consensus feature is broken for Anthropic providers.

**Context:** Codex outside-voice review discovered this: `minimax.go:240-245` selects compact schema for Anthropic profile via `reviewResultSchemaForProfile()`. The compact schema omits fields that `computeSemanticFingerprint()` in `dedup.go:490-497` relies on. Either the compact schema needs to include these fields (may increase token cost), or a post-parse extraction step needs to normalize findings from Anthropic responses into full-schema format. Must be resolved before P2 consensus work begins.

**Effort:** M
**Priority:** P0
**Depends on:** None — blocks P2 consensus

### Extend ProviderResponse for multi-provider observability

**What:** `ConsensusReviewService` implementing `Provider` interface means a single `ProviderResponse` must carry per-provider latency/token/audit data for 2-3 providers.

**Why:** Current `ProviderResponse` has single `Latency`, `Tokens`, `Model` fields. Hiding 3 providers behind one response loses per-provider observability in audit logs and metrics.

**Context:** Codex pointed this out: `processor.go:260-264` records one `response.Latency` and `response.Tokens`. Options: (1) Add `[]SubProviderResult` field to `ProviderResponse`, (2) ConsensusReviewService writes audit logs directly via side-channel before returning merged response, (3) Return aggregated totals in response + detailed breakdown in `ResponsePayload` map. Option 3 is simplest and backward-compatible.

**Effort:** S
**Priority:** P1
**Depends on:** ConsensusReviewService implementation

### Investigate existing CLI path before building cmd/review-cli

**What:** `cmd/manual-trigger` + `scripts/review-mr.sh` already provide CLI invocation with provider-route override and Docker-based local usage.

**Why:** Building a second CLI entry point may duplicate existing functionality. The simpler path might be to package the existing manual-trigger as a Docker image with documentation.

**Context:** Codex flagged this: the existing manual trigger requires DB (MySQL), but it exercises the full product pipeline (history, dedup, discussion memory). A new stateless CLI without DB is "a generic diff-to-LLM wrapper wearing the same name." Investigation should determine: (1) Can manual-trigger work with a lightweight in-memory/SQLite stub? (2) Is "full pipeline in Docker Compose" sufficient as lightweight deploy? (3) Is the true CLI need for "no persistence" or just "easy setup"?

**Effort:** S (investigation only)
**Priority:** P0
**Depends on:** None — informs CLI architecture decision

## Review Quality

### Review Dashboard (Grafana templates)

**What:** Provide Grafana dashboard templates for team-level review insights.

**Why:** Deferred from v1.0 CEO plan. Increases value for team leads who want review quality metrics.

**Context:** CEO plan scope decision #4. Audit logs already capture per-run latency, tokens, findings count, and provider model. Dashboard needs: (1) Grafana JSON dashboard templates, (2) MySQL → Grafana datasource queries, (3) Documentation for setup. Does not require code changes — pure dashboard templates.

**Effort:** M
**Priority:** P3
**Depends on:** None

## Infrastructure

### SQLite lightweight deploy mode

**What:** Replace MySQL with SQLite for single-machine deployment.

**Why:** Removes MySQL dependency for small teams. CEO plan Phase 2.

**Context:** Requires: (1) Database abstraction layer (current sqlc generates MySQL-specific queries), (2) goose migration SQLite dialect, (3) Redis replacement with in-memory token bucket rate limiting. Architecture project, not simple feature. ~13h CC estimated.

**Effort:** XL
**Priority:** P2
**Depends on:** CLI architecture decision (P0 investigation)

### Multi-model semantic dedup (LLM-based)

**What:** For cross-model findings that rough matching misses, use lightweight LLM call for semantic comparison.

**Why:** Improves consensus precision beyond canonical_key+symbol+line-window matching.

**Context:** CEO plan v1.1. Prompt skeleton: "Are these two code review findings describing the same issue? JSON: {same: bool, reason: string}". Failure fallback: treat as different issues (conservative). ~5h CC estimated.

**Effort:** M
**Priority:** P2
**Depends on:** P2 consensus rough matching

### Verify domestic model OpenAI compatibility

**What:** Test that DeepSeek and other "OpenAI-compatible" models handle `json_schema`, `strict`, `parallel_tool_calls`, and `reasoning_effort` fields correctly.

**Why:** Codex flagged that "zero code changes" is an unverified assumption. Many vendors only partially implement the OpenAI surface. If true, the domestic model moat is documentation; if false, adapter code is needed.

**Context:** `openai.go:127-173` sends these vendor-specific fields. Test with actual DeepSeek V3/R1 API calls. Record which features work, which silently fail, which error. May need conditional field emission per vendor.

**Effort:** S
**Priority:** P1
**Depends on:** None

## Completed

*No items completed yet.*
