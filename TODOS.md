# TODOS

## LLM / Consensus

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

## Completed

### Unify Anthropic compact schema for consensus matching
**Completed:** v0.19.2 (2025-03-25) — PR #14
Added `old_line`, `new_line`, `canonical_key`, `symbol`, `introduced_by_this_change` to `reviewFindingSchemaAnthropicCompact()` so Anthropic responses can participate in cross-model consensus dedup.

### Extend ProviderResponse for multi-provider observability
**Completed:** v0.19.2 (2025-03-25) — PR #15
Added `SubProviderResult` struct and `SubProviderResults []SubProviderResult` field to `ProviderResponse`. Updated `recordProviderMetrics()` to emit per-sub-provider histograms/counters and audit logs.

### Verify domestic model OpenAI compatibility
**Completed:** v0.19.2 (2025-03-25) — PR #16
Implemented `OpenAICompatMode` with per-feature toggles (`UseSystemRole`, `DropParallelToolCalls`, `DropStrictSchema`, `DropReasoningEffort`, `UseMaxTokens`). Added `DeepSeekCompatMode()` preset. Adapter code replaces assumption of "zero code changes."
