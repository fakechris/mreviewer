# TODOS

## LLM / Consensus

### Restore anchor_snippet / evidence in Anthropic compact schema

**What:** `reviewFindingSchemaAnthropicCompact()` (parser.go:490) drops `anchor_snippet`, `evidence`, `range_*`, `suggested_patch`, `trigger_condition`, `impact`, `blind_spots`.

**Why:** `computeAnchorFingerprint()` (dedup.go:511) uses `anchor_snippet`, and `reviewcore.evidenceFingerprint` falls back to `evidence`. Both degrade on Anthropic routes.

**Context (corrected 2026-10-07):** The earlier claim that compact schema strips `canonical_key`/`symbol`/line numbers was wrong; those fields are present, and `computeSemanticFingerprint()` (dedup.go:521) is unaffected. This no longer blocks consensus work. Amendment proposed on INV-329.

**Effort:** S
**Priority:** P2
**Depends on:** None

### Extend ProviderResponse for multi-provider observability

**What:** `ConsensusReviewService` implementing `Provider` interface means a single `ProviderResponse` must carry per-provider latency/token/audit data for 2-3 providers.

**Why:** Current `ProviderResponse` has single `Latency`, `Tokens`, `Model` fields. Hiding 3 providers behind one response loses per-provider observability in audit logs and metrics.

**Context:** Review analysis pointed this out: `processor.go:260-264` records one `response.Latency` and `response.Tokens`. Options: (1) Add `[]SubProviderResult` field to `ProviderResponse`, (2) ConsensusReviewService writes audit logs directly via side-channel before returning merged response, (3) Return aggregated totals in response + detailed breakdown in `ResponsePayload` map. Option 3 is simplest and backward-compatible.

**Effort:** S
**Priority:** P1
**Depends on:** ConsensusReviewService implementation
**Status note (2026-10-07):** `ProviderResponse.SubProviderResults` already exists (provider.go:64); remaining work is audit/metrics consumption.

## Review Quality

### Multi-model semantic dedup (LLM-based)

**What:** For cross-model findings that rough matching misses, use lightweight LLM call for semantic comparison.

**Why:** Improves consensus precision beyond canonical_key+symbol+line-window matching.

**Context:** CEO plan v1.1. Prompt skeleton: "Are these two code review findings describing the same issue? JSON: {same: bool, reason: string}". Failure fallback: treat as different issues (conservative). ~5h CC estimated.

**Effort:** S
**Priority:** P2
**Depends on:** P2 consensus rough matching
**Status note (2026-10-07):** `LLMSemanticMatcher` exists in semantic_match.go but is only mountable via legacy `Processor.WithSemanticMatcher`; remaining work is wiring.

### Verify domestic model OpenAI compatibility

**What:** Test that DeepSeek and other "OpenAI-compatible" models handle `json_schema`, `strict`, `parallel_tool_calls`, and `reasoning_effort` fields correctly.

**Why:** Prior review flagged that "zero code changes" is an unverified assumption. Many vendors only partially implement the OpenAI surface. If true, the domestic model moat is documentation; if false, adapter code is needed.

**Context:** `openai.go:127-173` sends these vendor-specific fields; `DeepSeekCompatMode` exists (openai.go:371). 2026-10 vendor status: DeepSeek strict schema only via `/beta` base URL + tool strict mode (no `response_format: json_schema`); GLM-5 `json_schema` silently ignored; Kimi K2.7 and Qwen3.5 (部分模型) support `json_schema` strict. Extend matrix to DeepSeek V4 + Kimi K2.7 + Qwen3.5. Record which features work, which silently fail, which error. May need conditional field emission per vendor.

**Effort:** S
**Priority:** P1
**Depends on:** None

## Review Trustworthiness (proposed 2026-10-07, see docs/plans/2026-10-07-roadmap-refresh.md)

### Finding line-anchor validation (INV-1018)
Reject or downgrade findings whose line is not in a changed hunk or deletion anchor before publishing. Today only the platform API reports the failure.
**Effort:** S **Priority:** P2

### Coverage evidence and `incomplete` verdict (INV-1019)
Expose which hunks/files were truncated or degraded in the brief and JSON; never emit `approved` below a coverage threshold.
**Effort:** M **Priority:** P2

### Local review-quality bench with LLM-as-judge (INV-1020)
`mreviewer bench review` over SWR-Bench/CR-Bench samples plus Chinese samples; outputs F1 and Chinese naturalness. Replaces the two-engineer blind review in INV-334.
**Effort:** M **Priority:** P2

### Single-model N-pass majority-vote consensus (INV-1021)
Bugbot-style N parallel passes with shuffled file order, grouped by the INV-331 rough matcher. First consumer of the consensus engine.
**Effort:** M **Priority:** P2

### Lens-style pluggable rule files (INV-1022)
Markdown + frontmatter triggers (file globs, hunk regex) replacing Go-constant reviewer packs; repo-level `.mreviewer/lenses/` override.
**Effort:** M **Priority:** P3

### CI setup-go 1.21 vs go.mod 1.25 (INV-1016, bug)
Use `go-version-file: go.mod` in ci.yml and release.yml.
**Effort:** XS **Priority:** P3

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
