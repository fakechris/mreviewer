# Webhook Control Plane Productization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Turn GitLab/GitHub webhook review from “it runs if healthy” into a productized, operator-friendly control plane with stable queue semantics, actionable dashboard surfaces, operator actions, and platform/user identity mapping.

**Architecture:** Keep the existing ingress -> reviewrun -> scheduler -> worker pipeline and make it production-grade instead of replacing it. Add stable read/write control-plane APIs on top of the current runtime, extend the DB schema for run lifecycle and identity mapping, and keep GitLab/GitHub behavior aligned behind shared concepts: run state, queue state, worker state, publish state, and actor identity.

**Tech Stack:** Go, existing `internal/adminapi`/`internal/adminui`, MySQL/SQLite SQL query layers, GitHub/GitLab platform clients, existing scheduler/reviewrun/hooks/githubhooks/runtime paths.

## Product Scope

This workstream is complete only when all of the following are true:

1. GitLab and GitHub webhook flows are stable under normal, retry, supersede, and worker-failure cases.
2. `/admin/` is a real operator surface, not just a thin JSON shell.
3. Operators can inspect queue state, active workers, failures, run details, and identity resolution from one place.
4. Operators can take bounded actions: retry failed run, rerun selected run, cancel stuck run, and requeue retry-eligible run.
5. Git authors/committers can be mapped to GitLab/GitHub platform users so the system can reason about who authored a change and who owns follow-up.
6. GitLab/GitHub runtime docs and real acceptance checklists are good enough for deployment without tribal knowledge.

## Non-Goals

- Do not redesign the core review engine or reviewer-pack orchestration.
- Do not expand benchmark/reporting work in this milestone.
- Do not add a large auth/permissions framework beyond the existing bearer-token admin gate.
- Do not build a full incident-management system; keep operator actions focused on review runs and identity resolution.

## Target User Stories

### Operator
- “Why is this MR/PR still waiting?”
- “Which worker is stuck?”
- “What failed in the last hour, by error code and platform?”
- “Can I retry or cancel this run without touching the DB?”

### Team Lead / Reviewer
- “Which GitLab/GitHub user actually corresponds to this git committer?”
- “Can I see the mapping between author email/name and platform user before assigning blame or ownership?”
- “Can I tell whether a failing review is from platform access, provider failure, or write-back?”

### On-call Engineer
- “Queue depth is rising. Is it provider latency, dead workers, or webhook duplication?”
- “Did latest-head-wins supersede the old run correctly?”
- “Is GitLab behavior diverging from GitHub?”

## Required Product Surfaces

### 1. Stable Runtime State Model

Normalize run lifecycle into explicit states and failure codes across GitLab/GitHub:

- queue-ish states:
  - `pending`
  - `running`
  - `completed`
  - `failed`
  - `cancelled`
- failure / terminal reasons:
  - `provider_failed`
  - `provider_timeout`
  - `publish_failed`
  - `worker_timeout`
  - `superseded_by_new_head`
  - `webhook_rejected`
  - `webhook_deduplicated`
  - `identity_resolution_failed`

Also expose sub-stage progress in a queryable form:
- `loading_target`
- `running_packs`
- `running_advisor`
- `publishing`
- `comparing_external`
- `comparing_targets`
- `completed`

### 2. Admin Dashboard / API

Expand `/admin/` and `/admin/api/*` into the following productized surfaces:

- `/admin/api/queue`
  - pending count
  - retry-scheduled count
  - running count
  - oldest waiting age
  - top queued projects/repos
  - superseded last 24h
  - optionally: per-platform queue split

- `/admin/api/concurrency`
  - active workers
  - configured concurrency per worker
  - running runs per worker
  - stale heartbeat warning
  - total configured capacity vs total active load

- `/admin/api/failures`
  - recent failed runs
  - failures by error code
  - webhook rejected / deduplicated
  - optionally: failures by platform and provider route

- **new** `/admin/api/runs`
  - recent runs list with filters:
    - platform
    - status
    - error_code
    - project/repo
    - head SHA
    - actor / mapped user

- **new** `/admin/api/runs/{id}`
  - full run detail:
    - lifecycle timestamps
    - queue wait
    - claimed worker
    - provider route
    - write-back result
    - audit trail summary
    - related hook event

- **new** `/admin/api/identity`
  - current author/committer ↔ platform user mappings
  - unresolved identities
  - mapping confidence/source

### 3. Operator Actions

Add minimal write endpoints behind the same admin bearer token:

- `POST /admin/api/runs/{id}/retry`
- `POST /admin/api/runs/{id}/rerun`
- `POST /admin/api/runs/{id}/cancel`
- `POST /admin/api/runs/{id}/requeue`
- `POST /admin/api/identity/resolve`
  - manually attach git author/committer identity to a platform user

All write actions must:
- write audit logs
- be idempotent where possible
- refuse invalid transitions
- return current run state after mutation

## Identity Mapping Scope

This is a core requirement for this milestone.

We need to support:

1. Git commit author/committer extraction
2. GitLab/GitHub platform user lookup
3. durable mapping storage
4. confidence / provenance tracking
5. dashboard visibility and manual correction

### Canonical Model

Introduce a canonical identity model:

- git identity:
  - `author_name`
  - `author_email`
  - `committer_name`
  - `committer_email`
  - optional: `git_username` if derivable
- platform identity:
  - `platform` (`gitlab` / `github`)
  - `platform_user_id`
  - `platform_username`
  - `display_name`
  - `email` if available
- mapping metadata:
  - `resolution_source` (`exact_email`, `username_match`, `manual`, `api_lookup`, `commit_author_field`)
  - `confidence`
  - `resolved_at`
  - `resolved_by`

### Mapping Use Cases

- Show the likely platform owner of a run.
- Attribute a run to the MR/PR author correctly.
- Detect “commit author != platform author” drift.
- Support future reviewer ownership/reporting without reworking the model.

### Minimum Resolution Strategy

Implement ordered matching:

1. exact email match
2. normalized username/login match
3. display-name fallback
4. manual admin resolution

Store unresolved candidates rather than silently dropping them.

## Implementation Tasks

### Task 1: Lock run-state and failure-code vocabulary

**Files:**
- Modify: `internal/reviewrun/service.go`
- Modify: `internal/reviewrun/engine_processor.go`
- Modify: `internal/scheduler/service.go`
- Modify: `internal/platform/gitlab/runtime_writeback.go`
- Modify: `internal/platform/github/runtime_writeback.go`
- Modify: `internal/gate/*.go`
- Test: `internal/reviewrun/service_test.go`
- Test: `internal/reviewrun/parity_test.go`
- Test: `internal/scheduler/service_test.go`

**Steps:**
1. Write failing tests for explicit run terminal/error transitions.
2. Verify they fail against current implicit behavior.
3. Add explicit error-code normalization helpers.
4. Thread normalized failure/state values through runtime outcomes.
5. Re-run focused tests.
6. Commit.

### Task 2: Add run detail read model

**Files:**
- Modify: `internal/db/queries/admin_dashboard.sql`
- Regenerate/update: `internal/db/admin_dashboard.sql.go`
- Modify: `internal/db/sqlitedb/admin_dashboard.go`
- Modify: `internal/adminapi/service.go`
- Modify: `internal/adminapi/handler.go`
- Test: `internal/adminapi/service_test.go`
- Test: `internal/adminapi/handler_test.go`
- Test: `internal/db/db_test.go`
- Test: `internal/db/sqlitedb/queries_test.go`

**Steps:**
1. Write failing tests for `ListRecentRuns` and `GetRunDetail`.
2. Add SQL queries for list/detail projections.
3. Expose service methods and HTTP endpoints.
4. Verify JSON response shape.
5. Commit.

### Task 3: Add operator write actions

**Files:**
- Modify: `internal/adminapi/handler.go`
- Add: `internal/adminapi/actions.go`
- Modify: `internal/reviewrun/service.go`
- Modify: `internal/scheduler/service.go`
- Modify: `internal/db/queries/review_runs.sql`
- Regenerate/update supporting DB code
- Test: `internal/adminapi/handler_test.go`
- Test: `internal/reviewrun/service_test.go`
- Test: `internal/scheduler/service_test.go`

**Steps:**
1. Write failing tests for retry/rerun/cancel/requeue transitions.
2. Add service-layer guardrails for legal transitions only.
3. Add admin POST handlers.
4. Log every action into audit trail.
5. Verify invalid transitions return 409/422 instead of mutating state.
6. Commit.

### Task 4: Productize `/admin/` UI

**Files:**
- Modify: `internal/adminui/template.html`
- Modify: `internal/adminui/handler.go`
- Test: `internal/adminui/handler_test.go`
- Docs: `docs/operations/admin-dashboard.md`

**Steps:**
1. Replace the current endpoint list shell with a real dashboard layout.
2. Add sections for queue, concurrency, failures, runs, and identity resolution.
3. Add basic fetch/render logic for new endpoints.
4. Add action affordances for retry/rerun/cancel/requeue.
5. Test auth and HTML rendering.
6. Commit.

### Task 5: Add identity mapping schema and store

**Files:**
- Add query file: `internal/db/queries/identity_mappings.sql`
- Add/modify generated DB code
- Add SQLite equivalent in `internal/db/sqlitedb/`
- Add: `internal/identity/` package
- Test: `internal/identity/*_test.go`
- Test: `internal/db/db_test.go`
- Test: `internal/db/sqlitedb/queries_test.go`

**Steps:**
1. Write failing tests for create/list/update mapping records.
2. Add durable schema for author/committer ↔ platform-user mappings.
3. Add service helpers for lookup by email/login/name.
4. Verify MySQL + SQLite paths.
5. Commit.

### Task 6: Extract author/committer identity from runtime inputs

**Files:**
- Modify: `internal/reviewcore/snapshot.go`
- Modify: `internal/platform/gitlab/adapter.go`
- Modify: `internal/platform/github/adapter.go`
- Modify: `internal/reviewinput/builder.go`
- Test: `internal/platform/gitlab/adapter_test.go`
- Test: `internal/platform/github/adapter_test.go`
- Test: `internal/reviewinput/builder_test.go`

**Steps:**
1. Write failing tests for preserving author/committer identity on snapshots.
2. Extend snapshot/build input with commit + platform actor identity.
3. Verify GitLab and GitHub adapters populate it consistently.
4. Commit.

### Task 7: Runtime identity resolution

**Files:**
- Add/modify: `internal/identity/resolver.go`
- Modify: `internal/reviewrun/engine_processor.go`
- Modify: `internal/platform/gitlab/runtime_writeback.go`
- Modify: `internal/platform/github/runtime_writeback.go`
- Modify: `internal/adminapi/service.go`
- Test: `internal/identity/resolver_test.go`
- Test: `internal/reviewrun/engine_processor_test.go`

**Steps:**
1. Write failing tests for exact-email, username, and unresolved fallback cases.
2. Resolve identities during runtime and persist mapping outcomes.
3. Surface resolved/unresolved identity on run detail APIs.
4. Emit `identity_resolution_failed` only when required mapping is mandatory and absent.
5. Commit.

### Task 8: Admin identity UI + manual resolution flow

**Files:**
- Modify: `internal/adminui/template.html`
- Modify: `internal/adminapi/handler.go`
- Modify: `internal/adminapi/service.go`
- Test: `internal/adminapi/handler_test.go`
- Docs: `docs/operations/admin-dashboard.md`

**Steps:**
1. Add unresolved identity list endpoint and UI section.
2. Add manual resolution POST endpoint.
3. Verify audit logging for manual resolution.
4. Confirm the UI can refresh and show resolved state.
5. Commit.

### Task 9: GitLab/GitHub runtime parity hardening

**Files:**
- Modify: `internal/hooks/*`
- Modify: `internal/githubhooks/*`
- Modify: `cmd/worker/main.go`
- Modify: `cmd/ingress/main.go`
- Test: `internal/hooks/handler_lifecycle_integration_test.go`
- Test: `internal/githubhooks/handler_test.go`
- Test: `cmd/worker/runtime_test.go`

**Steps:**
1. Add parity tests for dedupe, supersede, retry, and timeout across both platforms.
2. Verify worker heartbeats and stale-worker recovery with both GitLab and GitHub paths.
3. Normalize admin snapshots so platform differences do not leak into queue semantics.
4. Commit.

### Task 10: Product docs and operator acceptance matrix

**Files:**
- Modify: `README.md`
- Modify: `WEBHOOK.md`
- Modify: `docs/operations/gitlab-runtime.md`
- Modify: `docs/operations/github-runtime.md`
- Modify: `docs/operations/admin-dashboard.md`
- Modify: `docs/operations/failure-playbook.md`

**Steps:**
1. Document queue semantics and operator actions.
2. Document identity mapping behavior and manual override flow.
3. Add real acceptance steps for GitLab and GitHub webhook deploys.
4. Commit.

## Acceptance Matrix

### GitLab runtime
- webhook accepted
- webhook rejected on bad secret
- duplicate delivery deduplicated
- new head supersedes old pending run
- new head cancels old running run at safe boundary
- failed run can be retried from admin
- run detail page shows worker, route, failure, publish result
- unresolved author/committer mapping is visible in admin

### GitHub runtime
- webhook accepted
- webhook rejected on bad secret
- duplicate delivery deduplicated
- latest-head-wins matches GitLab semantics
- status transitions visible and correct
- failed run can be retried from admin
- run detail page shows worker, route, failure, publish result
- unresolved author/committer mapping is visible in admin

### Dashboard
- queue metrics are non-zero when queue is backed up
- stale workers are visible
- operator actions mutate state correctly
- auth gate works for read and write routes

### Identity mapping
- exact email maps automatically
- username fallback maps automatically
- unresolved identities appear in admin
- manual resolution fixes future lookups
- commit author != platform author is visible, not silently collapsed

## Rollout Order

1. stabilize runtime state + failure codes
2. add run list/detail read model
3. add operator write actions
4. productize `/admin/` UI
5. add identity mapping schema + resolver
6. add manual identity resolution
7. run GitLab/GitHub parity hardening
8. finish docs and production acceptance

## Recommended Execution Strategy

Do this in a dedicated implementation cycle, not mixed with benchmark work.

Recommended first batch:
- Task 1
- Task 2
- Task 3

That batch gets the system from “observable” to “operable”, which is the real prerequisite for the rest.
