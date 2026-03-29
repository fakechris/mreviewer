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

## Review Quality

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

### Open Source Readiness Package — v0.21.0

**Completed:** 2026-03-29
**What:** Essential files for open source release: bilingual documentation, license, contribution guidelines, CI.
**Delivered:**
- Bilingual README (English + Chinese)
- MIT License
- CONTRIBUTING.md
- GitHub Actions CI workflow
- QUICKSTART.md for core functionality verification

### Review Dashboard (Grafana templates) — v0.20.0

**Completed:** 2026-03-27
**What:** Grafana JSON dashboard templates for review operations, provider performance, and finding quality.
**Delivered:** `grafana/dashboards/review-operations.json`, `grafana/dashboards/provider-performance.json`, `grafana/dashboards/finding-quality.json`, `grafana/README.md`

### SQLite lightweight deploy mode — v0.20.0

**Completed:** 2026-03-27
**What:** Full SQLite backend alternative to MySQL for single-machine deployment.
**Delivered:**
- `db.Store` interface abstracting MySQL/SQLite behind a common API
- DSN-based dialect auto-detection (`sqlite://` prefix → SQLite, else MySQL)
- Hand-written SQLite Querier (`internal/db/sqlitedb/`) — 60+ methods returning shared `db.*` model types
- SQLite migrations (`migrations_sqlite/001_create_core_tables.sql`)
- `database.StoreFactory(dialect)` for runtime store construction
- All production call sites updated: scheduler, hooks, llm processor, writer, gate, manualtrigger, worker, ingress
- 14 SQLite CRUD tests + 571 total tests passing

### Investigate existing CLI path before building cmd/review-cli — v0.20.0

**Completed:** 2026-03-25
**What:** Investigation concluded that SQLite deploy mode satisfies the "easy setup" need. The existing `cmd/manual-trigger` + full pipeline with SQLite provides a better experience than a stateless CLI wrapper.
**Outcome:** SQLite mode chosen over a new cmd/review-cli. Full pipeline (history, dedup, discussion memory) preserved.
