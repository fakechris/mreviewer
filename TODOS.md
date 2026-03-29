# TODOS

## LLM / Consensus

### Verify domestic model OpenAI compatibility

**What:** Test that DeepSeek and other "OpenAI-compatible" models handle `json_schema`, `strict`, `parallel_tool_calls`, and `reasoning_effort` fields correctly.

**Why:** Codex flagged that "zero code changes" is an unverified assumption. Many vendors only partially implement the OpenAI surface. If true, the domestic model moat is documentation; if false, adapter code is needed.

**Context:** `openai.go:127-173` sends these vendor-specific fields. Test with actual DeepSeek V3/R1 API calls. Record which features work, which silently fail, which error. May need conditional field emission per vendor.

**Effort:** S
**Priority:** P1
**Depends on:** None

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

### Anthropic compact schema consensus fields — v0.21.0

**Completed:** 2026-03-29
**What:** Added `canonical_key` and `symbol` fields to Anthropic compact schema for consensus matching.
**Delivered:** Commit 7720ce2. Fields now included in `reviewFindingSchemaAnthropicCompact()` (parser.go:465-466), enabling Anthropic providers to participate in cross-model consensus.

### Multi-provider observability in ProviderResponse — v0.21.0

**Completed:** 2026-03-29
**What:** Added `SubProviderResults` field to capture per-provider metrics for composite providers.
**Delivered:** `SubProviderResults []SubProviderResult` field in ProviderResponse (provider.go:65), used in processor.go:267-277 for audit logging with per-provider latency/tokens/status.

### Fireworks Kimi model compatibility verification — v0.21.0

**Completed:** 2026-03-29
**What:** Verified Fireworks AI Kimi-k2.5-turbo model works with Anthropic SDK.
**Result:** Model `fireworks_ai/accounts/fireworks/models/kimi-k2p5` fully compatible. Returns thinking block + text block. Chinese responses verified.

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
