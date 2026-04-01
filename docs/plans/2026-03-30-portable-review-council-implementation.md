# Portable Review Council Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a CLI-first, platform-neutral review engine that accepts GitHub/GitLab PR URLs, runs specialist reviewer packs plus a judge, emits Markdown/JSON artifacts, and optionally writes back full review comments.

**Architecture:** Introduce a new artifact-first engine in parallel with the current GitLab-first pipeline. Migrate stable logic into shared contracts and builders, cut `manual-trigger` over first, then move webhook ingestion onto the same engine. Preserve existing review behavior via explicit parity tests against the current GitLab path.

**Tech Stack:** Go, existing GitLab client/store/rules/context assembly pipeline, new platform adapters, canonical review artifacts, existing provider registry, existing writer logic migrated behind platform writers.

## Implementation Principles

- Extract and relocate stable behavior; do not rewrite review semantics unless migration exposes a hard blocker.
- Keep the new engine platform-neutral and artifact-first.
- Treat GitHub and GitLab as equal first-class platforms in phase 1.
- Expose the new product as a standalone CLI, not as a skill.
- Gate cutovers with behavior parity tests against the current GitLab path.

## Proposed Package Layout

**Create:**
- `cmd/mreviewer/main.go`
- `cmd/mreviewer/main_test.go`
- `internal/reviewcore/target.go`
- `internal/reviewcore/input.go`
- `internal/reviewcore/location.go`
- `internal/reviewcore/finding.go`
- `internal/reviewcore/artifact.go`
- `internal/reviewcore/bundle.go`
- `internal/reviewcore/engine.go`
- `internal/reviewcore/engine_test.go`
- `internal/reviewinput/builder.go`
- `internal/reviewinput/builder_test.go`
- `internal/platform/gitlab/adapter.go`
- `internal/platform/gitlab/adapter_test.go`
- `internal/platform/gitlab/writer.go`
- `internal/platform/gitlab/writer_test.go`
- `internal/platform/github/client.go`
- `internal/platform/github/client_test.go`
- `internal/platform/github/adapter.go`
- `internal/platform/github/adapter_test.go`
- `internal/platform/github/writer.go`
- `internal/platform/github/writer_test.go`
- `internal/reviewpack/pack.go`
- `internal/reviewpack/security.go`
- `internal/reviewpack/architecture.go`
- `internal/reviewpack/database.go`
- `internal/reviewpack/pack_test.go`
- `internal/judge/engine.go`
- `internal/judge/engine_test.go`
- `internal/compare/artifact.go`
- `internal/compare/artifact_test.go`
- `internal/compare/ingest_github.go`
- `internal/compare/ingest_gitlab.go`
- `internal/compare/ingest_test.go`
- `internal/reviewrun/service.go`
- `internal/reviewrun/service_test.go`
- `internal/reviewrun/parity_test.go`

**Modify:**
- `cmd/manual-trigger/main.go`
- `cmd/manual-trigger/main_test.go`
- `cmd/ingress/main.go`
- `cmd/worker/main.go`
- `internal/manualtrigger/service.go`
- `internal/hooks/handler.go`
- `internal/llm/processor.go`
- `internal/gitlab/client.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/writer/writer.go`
- `internal/writer/writer_test.go`
- `README.md`
- `README.zh-CN.md`
- `WEBHOOK.md`

## Task 1: Define Canonical Contracts

**Files:**
- Create: `internal/reviewcore/target.go`
- Create: `internal/reviewcore/input.go`
- Create: `internal/reviewcore/location.go`
- Create: `internal/reviewcore/finding.go`
- Create: `internal/reviewcore/artifact.go`
- Create: `internal/reviewcore/bundle.go`
- Test: `internal/reviewcore/engine_test.go`

**Step 1: Write failing contract tests**

Add table-driven tests that assert:
- `ReviewTarget` can represent both GitHub PR and GitLab MR URLs.
- `CanonicalLocation` supports file path, side, line/range, snippet, metadata blob.
- `FindingIdentityInput` contains location, category, normalized claim, evidence fingerprint, severity tags.
- `ReviewerArtifact` and `ReviewBundle` can serialize to stable JSON.

**Step 2: Run tests to verify failure**

Run: `go test ./internal/reviewcore -count=1`
Expected: FAIL with missing package or missing types.

**Step 3: Implement minimal contracts**

Define:
- `type ReviewTarget struct`
- `type ReviewInput struct`
- `type CanonicalLocation struct`
- `type Finding struct`
- `type ReviewerArtifact struct`
- `type ReviewBundle struct`

Include explicit fields for:
- platform identity
- target identity
- reviewer pack identity
- publish candidates
- comparison artifacts

**Step 4: Run tests to verify pass**

Run: `go test ./internal/reviewcore -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/reviewcore
git commit -m "feat: add canonical review engine contracts"
```

## Task 2: Add PlatformSnapshot Boundary for GitLab

**Files:**
- Create: `internal/platform/gitlab/adapter.go`
- Create: `internal/platform/gitlab/adapter_test.go`
- Modify: `internal/gitlab/client.go`
- Test: `internal/platform/gitlab/adapter_test.go`

**Step 1: Write failing adapter tests**

Add tests for:
- PR/MR URL parsing into `ReviewTarget`
- `PlatformAdapter.FetchSnapshot(...)`
- snapshot contents preserving MR metadata, diffs, version refs, and platform-specific anchor metadata

**Step 2: Run tests to verify failure**

Run: `go test ./internal/platform/gitlab -count=1`
Expected: FAIL with missing adapter/snapshot types.

**Step 3: Implement GitLab PlatformSnapshot adapter**

Create a thin adapter over existing `internal/gitlab.Client` that:
- resolves `ReviewTarget`
- fetches merge request, version, diffs
- emits `PlatformSnapshot`
- preserves platform metadata instead of transforming into writer payloads

Do not rewrite fetch semantics in `internal/gitlab/client.go`; only extract or wrap.

**Step 4: Run tests**

Run: `go test ./internal/platform/gitlab -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/platform/gitlab internal/gitlab/client.go
git commit -m "feat: add gitlab platform snapshot adapter"
```

## Task 3: Migrate Stable InputBuilder Logic

**Files:**
- Create: `internal/reviewinput/builder.go`
- Create: `internal/reviewinput/builder_test.go`
- Modify: `internal/llm/processor.go`
- Test: `internal/reviewinput/builder_test.go`

**Step 1: Write parity-oriented builder tests**

Add tests that compare:
- legacy assembled request fields
- new `ReviewInput` fields

Cover:
- rules loading
- output language shaping
- policy settings
- historical context loading
- diff/context assembly

**Step 2: Run tests to verify failure**

Run: `go test ./internal/reviewinput -count=1`
Expected: FAIL with missing builder or parity fixtures.

**Step 3: Implement `InputBuilder`**

Move stable logic behind:
- `Build(ctx, snapshot, options) (reviewcore.ReviewInput, error)`

Reuse existing:
- `rulesLoader`
- `reviewlang`
- `context.Assembler`
- policy parsing
- historical context loading

Do not change provider payload semantics yet.

**Step 4: Add regression coverage**

Extract one or two representative GitLab fixtures from the current path and assert no meaningful drift in:
- changed files
- prompt context sections
- output language
- effective route/policy inputs

**Step 5: Run tests**

Run: `go test ./internal/reviewinput ./internal/llm -count=1`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/reviewinput internal/llm/processor.go
git commit -m "refactor: migrate review context assembly into input builder"
```

## Task 4: Create Product-Grade CLI with GitLab Artifact-Only Path

**Files:**
- Create: `cmd/mreviewer/main.go`
- Create: `cmd/mreviewer/main_test.go`
- Create: `internal/reviewcore/engine.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write CLI contract tests**

Cover:
- GitLab PR URL input
- `--output markdown|json|both`
- `--publish artifact-only`
- reviewer pack selection
- route override
- BYOK config loading

**Step 2: Run tests to verify failure**

Run: `go test ./cmd/mreviewer ./internal/reviewcore ./internal/config -count=1`
Expected: FAIL with missing command or flags.

**Step 3: Implement thin engine skeleton**

Add:
- `Engine.Run(ctx, target, options) (ReviewBundle, error)`

For this task, only support:
- GitLab target
- artifact-only
- no judge yet
- no platform write-back yet

Use the new GitLab adapter + InputBuilder path.

**Step 4: Implement `cmd/mreviewer`**

Support:
- PR/MR URL input
- output mode
- publish mode
- pack selection flag plumbing
- route override plumbing

For now, return a valid artifact bundle even if only a single default pack is wired.

**Step 5: Run tests**

Run: `go test ./cmd/mreviewer ./internal/reviewcore ./internal/config -count=1`
Expected: PASS

**Step 6: Commit**

```bash
git add cmd/mreviewer internal/reviewcore internal/config
git commit -m "feat: add review council cli with gitlab artifact path"
```

## Task 5: Implement Capability Packs and Decision-Engine Judge

**Files:**
- Create: `internal/reviewpack/pack.go`
- Create: `internal/reviewpack/security.go`
- Create: `internal/reviewpack/architecture.go`
- Create: `internal/reviewpack/database.go`
- Create: `internal/reviewpack/pack_test.go`
- Create: `internal/judge/engine.go`
- Create: `internal/judge/engine_test.go`
- Modify: `internal/reviewcore/engine.go`

**Step 1: Write pack contract tests**

Cover:
- each pack declares identity, focus, severity rubric, standards hooks
- pack outputs `ReviewerArtifact`
- security pack carries OWASP/ASVS standards metadata

**Step 2: Write judge tests**

Cover:
- dedupe overlapping findings
- merge canonical finding identities
- final verdict synthesis
- disagreement handling

**Step 3: Run tests to verify failure**

Run: `go test ./internal/reviewpack ./internal/judge ./internal/reviewcore -count=1`
Expected: FAIL with missing pack/judge implementation.

**Step 4: Implement capability packs**

Create pack interfaces that return:
- declared contract
- provider request inputs
- artifact outputs

Start with:
- `security`
- `architecture`
- `database`

**Step 5: Implement judge as decision engine**

Judge should:
- group findings by canonical identity
- retain evidence references
- emit merged finding groups
- produce final verdict and summary inputs

**Step 6: Wire packs + judge into engine**

Update `Engine.Run(...)` to:
- run selected packs
- collect `ReviewerArtifact`s
- run judge
- build `ReviewBundle`

**Step 7: Run tests**

Run: `go test ./internal/reviewpack ./internal/judge ./internal/reviewcore -count=1`
Expected: PASS

**Step 8: Commit**

```bash
git add internal/reviewpack internal/judge internal/reviewcore
git commit -m "feat: add specialist reviewer packs and decision judge"
```

## Task 6: Add GitLab PlatformWriter and Cut Over Manual Trigger

**Files:**
- Create: `internal/platform/gitlab/writer.go`
- Create: `internal/platform/gitlab/writer_test.go`
- Modify: `internal/manualtrigger/service.go`
- Modify: `cmd/manual-trigger/main.go`
- Modify: `cmd/manual-trigger/main_test.go`
- Modify: `internal/writer/writer.go`

**Step 1: Write GitLab writer tests**

Cover:
- summary comment mapping
- inline comment anchor translation
- canonical publish candidate to GitLab payload translation

**Step 2: Write manual-trigger parity tests**

Add tests that compare:
- old manual-trigger run output
- new engine-backed manual-trigger run output

**Step 3: Run tests to verify failure**

Run: `go test ./internal/platform/gitlab ./internal/manualtrigger ./cmd/manual-trigger -count=1`
Expected: FAIL with missing writer or parity assertions.

**Step 4: Implement GitLab PlatformWriter**

Translate `ReviewBundle.PublishCandidates` into:
- summary note
- inline review comments

Reuse current writer behavior where possible instead of redesigning.

**Step 5: Cut `manual-trigger` onto new engine**

Refactor `internal/manualtrigger/service.go` so it:
- resolves GitLab target
- invokes the new engine
- persists run state as needed

Preserve current CLI flags and JSON output contract.

**Step 6: Run tests**

Run: `go test ./internal/platform/gitlab ./internal/manualtrigger ./cmd/manual-trigger -count=1`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/platform/gitlab internal/manualtrigger cmd/manual-trigger internal/writer
git commit -m "refactor: migrate manual trigger onto review engine"
```

## Task 7: Add GitHub Platform Adapter and Writer

**Files:**
- Create: `internal/platform/github/client.go`
- Create: `internal/platform/github/client_test.go`
- Create: `internal/platform/github/adapter.go`
- Create: `internal/platform/github/adapter_test.go`
- Create: `internal/platform/github/writer.go`
- Create: `internal/platform/github/writer_test.go`
- Modify: `internal/reviewcore/engine.go`
- Modify: `cmd/mreviewer/main.go`

**Step 1: Write GitHub adapter tests**

Cover:
- PR URL parsing
- PR snapshot fetch
- diff mapping
- comment/review anchor mapping

**Step 2: Write GitHub writer tests**

Cover:
- summary review body generation
- inline review comment translation
- canonical publish candidate mapping

**Step 3: Run tests to verify failure**

Run: `go test ./internal/platform/github ./cmd/mreviewer -count=1`
Expected: FAIL with missing GitHub adapter/writer.

**Step 4: Implement GitHub adapter and writer**

Use a dedicated GitHub client package.
Normalize GitHub data into `PlatformSnapshot` and translate publish candidates out through `PlatformWriter`.

**Step 5: Update CLI to dispatch by platform**

Support:
- GitLab MR URL
- GitHub PR URL

**Step 6: Run tests**

Run: `go test ./internal/platform/github ./cmd/mreviewer ./internal/reviewcore -count=1`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/platform/github cmd/mreviewer internal/reviewcore
git commit -m "feat: add github review adapter and writer"
```

## Task 8: Implement Live Comparison Ingestion and Canonical Artifact Normalization

**Files:**
- Create: `internal/compare/artifact.go`
- Create: `internal/compare/artifact_test.go`
- Create: `internal/compare/ingest_github.go`
- Create: `internal/compare/ingest_gitlab.go`
- Create: `internal/compare/ingest_test.go`
- Modify: `cmd/mreviewer/main.go`

**Step 1: Write comparison normalization tests**

Cover:
- live external reviewer comments ingested into `ReviewerArtifact`
- canonical finding identity generation
- agreement/unique finding computation

Use fixtures for:
- external reviewer comments
- CodeRabbit-like comments
- generic bot comments

**Step 2: Run tests to verify failure**

Run: `go test ./internal/compare ./cmd/mreviewer -count=1`
Expected: FAIL with missing comparison pipeline.

**Step 3: Implement artifact normalization**

Add:
- comparison artifact types
- reviewer identity metadata
- agreement/unique finding logic

**Step 4: Implement GitHub/GitLab live ingestion**

Translate external review comments from each platform into canonical `ReviewerArtifact`s.

**Step 5: Add CLI comparison flags**

Support:
- compare live reviewers on target PR/MR
- import external artifact file(s)
- output comparison in Markdown and JSON

**Step 6: Run tests**

Run: `go test ./internal/compare ./cmd/mreviewer -count=1`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/compare cmd/mreviewer
git commit -m "feat: add reviewer comparison ingestion and normalization"
```

## Task 9: Migrate Webhook Path to the New Engine

**Files:**
- Create: `internal/reviewrun/service.go`
- Create: `internal/reviewrun/service_test.go`
- Create: `internal/reviewrun/parity_test.go`
- Modify: `internal/hooks/handler.go`
- Modify: `cmd/ingress/main.go`
- Modify: `cmd/worker/main.go`
- Modify: `internal/llm/processor.go`

**Step 1: Write webhook parity tests**

Cover:
- old webhook path review lifecycle
- new engine-backed webhook lifecycle
- run state transitions
- retry/cancel/supersede invariants

**Step 2: Run tests to verify failure**

Run: `go test ./internal/reviewrun ./internal/hooks ./cmd/worker -count=1`
Expected: FAIL with missing orchestrator or parity cases.

**Step 3: Implement review run orchestrator**

Create a thin orchestration service that:
- accepts normalized events
- resolves target
- invokes engine
- persists run metadata
- delegates publish via platform writer

Preserve:
- latest-head-wins behavior
- audit hooks
- metrics hooks

**Step 4: Cut webhook path onto orchestrator**

Move `internal/hooks/handler.go` and worker execution onto the new review engine/orchestrator path without changing webhook contract.

**Step 5: Run tests**

Run: `go test ./internal/reviewrun ./internal/hooks ./cmd/worker ./internal/llm -count=1`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/reviewrun internal/hooks cmd/ingress cmd/worker internal/llm
git commit -m "refactor: migrate webhook processing onto review engine"
```

## Task 10: Add End-to-End Parity Matrix, Docs, and Launch Contract Verification

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Modify: `WEBHOOK.md`
- Modify: `cmd/mreviewer/main_test.go`
- Modify: `internal/reviewrun/parity_test.go`

**Step 1: Write parity matrix doc section**

Document:
- GitLab legacy path vs new path checks
- manual-trigger parity
- webhook parity
- GitHub path validation
- CLI output contract

**Step 2: Add end-to-end golden tests**

Cover:
- GitLab artifact-only CLI
- GitLab full-review-comments CLI
- GitHub artifact-only CLI
- GitHub full-review-comments CLI
- comparison output JSON schema

**Step 3: Run full verification**

Run:

```bash
go test ./... -count=1
```

And run focused smoke commands:

```bash
go test ./cmd/mreviewer ./cmd/manual-trigger ./internal/reviewrun ./internal/platform/gitlab ./internal/platform/github ./internal/compare -count=1
```

Expected: PASS

**Step 4: Update docs**

Update:
- quick-start CLI usage
- BYOK/private gateway setup
- GitHub/GitLab usage examples
- publish mode examples
- comparison examples
- webhook path now backed by the shared engine

**Step 5: Commit**

```bash
git add README.md README.zh-CN.md WEBHOOK.md cmd/mreviewer internal/reviewrun internal/platform internal/compare
git commit -m "docs: publish portable review council cli contract"
```

## Test Matrix

- `go test ./internal/reviewcore -count=1`
- `go test ./internal/platform/gitlab -count=1`
- `go test ./internal/reviewinput ./internal/llm -count=1`
- `go test ./cmd/mreviewer ./internal/reviewcore ./internal/config -count=1`
- `go test ./internal/reviewpack ./internal/judge ./internal/reviewcore -count=1`
- `go test ./internal/platform/gitlab ./internal/manualtrigger ./cmd/manual-trigger -count=1`
- `go test ./internal/platform/github ./cmd/mreviewer ./internal/reviewcore -count=1`
- `go test ./internal/compare ./cmd/mreviewer -count=1`
- `go test ./internal/reviewrun ./internal/hooks ./cmd/worker ./internal/llm -count=1`
- `go test ./... -count=1`

## Cutover Checkpoints

- Checkpoint 1: GitLab artifact-only CLI runs on new engine without write-back.
- Checkpoint 2: `manual-trigger` runs on new engine with parity against the old path.
- Checkpoint 3: GitLab write-back runs through `PlatformWriter` with parity.
- Checkpoint 4: GitHub PR URL path works end-to-end through the same engine.
- Checkpoint 5: live external reviewer comparison works on both platforms.
- Checkpoint 6: webhook path is migrated with parity tests and smoke coverage.

## Out of Scope for This Plan

- local diff / branch review
- full per-pack + per-judge + per-comparison model routing
- marketplace/distribution through skill surfaces
- outcome benchmark as a primary quality metric
