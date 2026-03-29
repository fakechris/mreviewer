# TODOS

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
**Depends on:** ProcessorStore interface (completed)

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

### Investigate existing CLI path before building cmd/review-cli
**Completed:** v0.19.3 (2026-03-26)
Investigation found `cmd/manual-trigger` exercises full pipeline requiring MySQL (15+ queries). Extracted `ProcessorStore` interface as foundation for future stateless CLI / SQLite backend. Separate `cmd/review-cli` not needed — `ProcessorStore` enables swappable storage.

### Multi-model semantic dedup (LLM-based)
**Completed:** v0.19.3 (2026-03-26)
Added three-pass finding dedup: anchor fingerprint → semantic fingerprint → LLM-based comparison via `SemanticMatcher` interface. `LLMSemanticMatcher` calls OpenAI-compatible endpoint. Conservative fallback on errors. 9 new tests (unit + integration).
