# Progress

## 2026-03-23

- Reviewed the received architecture/code-review feedback against `main`.
- Confirmed high-priority correctness issues:
  - gate uses free-text parsing for resolved state
  - writer parses range anchors from `evidence`
- Confirmed performance issues:
  - writeback is sequential
  - single worker process handles one run at a time
- Confirmed several architecture issues; noted that some older review points are already obsolete on current `main`.
- Started Phase 1 with a TDD-first approach.
phase1 tests added
- Phase 1 completed:
  - gate now prefers structured `gitlab_discussions.resolved`
  - findings persist structured range anchor fields and writer consumes them before legacy `evidence` fallback
- Phase 3 completed:
  - loader now explicitly rejects instructions from untrusted content
  - suspicious-source detection now catches broader prompt-injection, exfiltration, and policy-bypass variants
- Phase 2 completed:
  - writer now processes finding writeback with bounded concurrency
  - scheduler now runs multiple worker loops per process with configurable concurrency
- Phase 4 partially completed:
  - duplicate-key detection moved into `internal/db`
  - GitLab discussion request/response types moved to `internal/reviewcomment`
  - command passing between hooks and commands now uses shared `internal/notecommand` types instead of `interface{}`
  - `internal/llm/provider.go` still needs a structural file split

## 2026-03-27

- P3 Grafana Dashboard templates completed:
  - `grafana/dashboards/review-operations.json` — throughput, success rate, error distribution
  - `grafana/dashboards/provider-performance.json` — latency percentiles, token consumption trends
  - `grafana/dashboards/finding-quality.json` — severity distribution, confidence analysis
  - `grafana/README.md` — MySQL datasource config + import instructions
- P2 SQLite lightweight deploy mode completed (all 5 stages):
  - Stage 1: `db.Store` interface + `database.Dialect` type + DSN auto-detection + `modernc.org/sqlite` driver
  - Stage 2: SQLite migrations (`migrations_sqlite/001_create_core_tables.sql`) — 11 tables translated from MySQL DDL
  - Stage 3: Hand-written SQLite Querier (`internal/db/sqlitedb/`) — 60+ methods, 14 CRUD tests passing
    - Key translations: `ON DUPLICATE KEY UPDATE` → `ON CONFLICT DO UPDATE`, `INTERVAL` → `datetime()`, removed `FOR UPDATE SKIP LOCKED`
    - `jsonScanner` wrapper for SQLite TEXT→json.RawMessage scanning
  - Stage 4: `database.NewStore()` factory, `database.StoreFactory()` for runtime, `config.DSN()` backward-compatible config
  - Stage 5: All production call sites updated to use store factory pattern:
    - scheduler (11 calls), hooks handler (5 calls), llm processor (2 calls + DBAuditLogger), writer, gate, manualtrigger, worker runtime, ingress/worker main
    - Changed concrete `*db.Queries` fields to `db.Store` interface across llm/processor, llm/dedup, llm/summary, writer/sql_store, gate/service
  - 571 tests passing (only pre-existing MySQL connectivity test fails without MySQL server)
- Strategic direction updated: SQLite mode moved from v1.1 to completed; Grafana dashboards completed
