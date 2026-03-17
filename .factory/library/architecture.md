# Architecture

Architectural decisions and implementation patterns for this mission.

**What belongs here:** service boundaries, core data flow, package design, persistence patterns, and hard architectural choices.

---

## System shape

- Go backend service for GitLab MR review automation
- Webhook-driven ingestion model
- Durable lifecycle centered on `review_runs` and `review_findings`
- GitLab writeback through discussions and optional gate adapters

## Core flow

1. Webhook arrives and is verified.
2. Event is normalized into a stable internal trigger.
3. A `review_run` is created or deduplicated.
4. Scheduler claims the run.
5. GitLab data is fetched and context assembled.
6. Provider returns structured findings.
7. Findings are normalized, deduped, and state-transitioned.
8. Discussions, summaries, and optional gate outputs are published.

## Package-level guidance

- Keep `cmd/*` thin.
- Use `internal/` packages for all app code.
- Prefer `sqlc` for static SQL and `sqlx` only for dynamic filtering needs.
- Treat GitLab and provider integrations as adapters behind interfaces.

## Reliability guidance

- Use transaction boundaries for multi-row state changes.
- When `head_sha` is unavailable at webhook-ingest time, keep deferred trigger recovery context by linking `review_runs.hook_event_id` back to the stored `hook_events.payload`; later recovery should resolve the real SHA from that persisted payload instead of collapsing distinct deferred triggers onto one synthetic key.
- Favor DB-backed correctness over Redis-backed coordination.
- Retry-scheduled runs remain active work: `review_runs.status='failed'` with a due `next_retry_at` is still claimable by the scheduler, so lifecycle/cancellation logic must treat those rows like pending work until retries are exhausted or the run is cancelled.
- All writeback actions must be idempotent and auditable.
- Keep machine-readable error codes for parser, anchor, provider, and writer failures.
