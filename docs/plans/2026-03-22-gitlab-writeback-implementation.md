# GitLab Writeback Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make production `worker` write review findings back to GitLab discussions/notes after a run completes.

**Architecture:** Add a real GitLab discussion client that satisfies `writer.DiscussionClient`, then wrap the scheduler processor in the worker runtime so successful runs flow through `writer.Write(...)` before the scheduler completes the cycle. Keep LLM provider wiring on the existing Anthropic-compatible MiniMax path, matching `gitanalyse`'s `MINIMAX_API_KEY` convention through env mapping rather than inventing a second provider stack.

**Tech Stack:** Go, GitLab REST API v4, existing `internal/gitlab`, `internal/writer`, `internal/scheduler`, MySQL-backed integration tests.

### Task 1: Lock runtime writeback behavior with failing tests

**Files:**
- Modify: `cmd/worker/runtime_test.go`
- Test: `cmd/worker/runtime_test.go`

**Step 1: Write the failing test**

Add runtime coverage that uses a production-style runtime wrapper and asserts:
- a `writer` is invoked automatically after processor success
- inline findings become GitLab discussion requests
- summary notes are written for terminal runs

**Step 2: Run test to verify it fails**

Run: `go test -run TestWorkerRuntimeWritesBackFindings ./cmd/worker`

**Step 3: Write minimal implementation**

Implement runtime wiring so `newRuntimeDeps(...)` can accept and invoke a writer-backed post-processor.

**Step 4: Run test to verify it passes**

Run: `go test -run TestWorkerRuntimeWritesBackFindings ./cmd/worker`

### Task 2: Lock GitLab discussion client behavior with failing tests

**Files:**
- Modify: `internal/gitlab/client_test.go`
- Modify: `internal/gitlab/client.go`
- Test: `internal/gitlab/client_test.go`

**Step 1: Write the failing tests**

Add tests for:
- `CreateDiscussion` posting to MR discussions endpoint with position payload
- `CreateNote` posting to MR notes endpoint
- `ResolveDiscussion` toggling resolved state through discussion notes API

**Step 2: Run test to verify they fail**

Run: `go test -run 'TestCreateDiscussion|TestCreateNote|TestResolveDiscussion' ./internal/gitlab`

**Step 3: Write minimal implementation**

Add a production `DiscussionClient` implementation to `internal/gitlab/client.go` reusing existing auth, retry, and request helpers.

**Step 4: Run test to verify they pass**

Run: `go test -run 'TestCreateDiscussion|TestCreateNote|TestResolveDiscussion' ./internal/gitlab`

### Task 3: Wire the production worker path

**Files:**
- Modify: `cmd/worker/main.go`
- Modify: `cmd/worker/runtime.go`
- Modify: `cmd/worker/runtime_test.go`

**Step 1: Write the failing test**

Add or extend a runtime test that boots worker deps the same way as `main.go` and verifies the writer is instantiated and used.

**Step 2: Run test to verify it fails**

Run: `go test -run TestWorkerRuntimeInjectsWriteback ./cmd/worker`

**Step 3: Write minimal implementation**

Instantiate the GitLab writer client in `main.go`, pass it into runtime wiring, and wrap processor outcomes with `writer.Write(...)`.

**Step 4: Run test to verify it passes**

Run: `go test -run TestWorkerRuntimeInjectsWriteback ./cmd/worker`

### Task 4: End-to-end persistence-to-writeback regression

**Files:**
- Modify: `cmd/worker/runtime_test.go`
- Possibly modify: `internal/e2e/database_failure_recovery_test.go`

**Step 1: Write the failing regression**

Add an integration-style runtime test proving:
- a run is claimed
- processor returns persisted findings
- runtime path writes discussions/notes
- comment action and discussion rows are persisted

**Step 2: Run test to verify it fails**

Run: `go test -run TestWorkerRuntimePersistedFindingsWriteBack ./cmd/worker`

**Step 3: Write minimal implementation**

Fill any missing store or runtime glue until the regression passes.

**Step 4: Run test to verify it passes**

Run: `go test -run TestWorkerRuntimePersistedFindingsWriteBack ./cmd/worker`

### Task 5: Documentation and config cleanup

**Files:**
- Modify: `README.md`
- Modify: `.env.example`

**Step 1: Update docs**

Document:
- review output language config
- current MiniMax env convention
- that worker now writes back GitLab discussions/notes

**Step 2: Verify docs against implementation**

Run:
- `go build ./cmd/worker ./cmd/ingress ./cmd/manual-trigger`
- `go test ./internal/gitlab ./internal/writer ./cmd/worker`

