# GitLab External Commit Status Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add GitLab external commit status writeback so merge requests show `running` while AI review is executing and `success` or `failed` when it finishes.

**Architecture:** Reuse the existing scheduler and gate lifecycle. Add a GitLab commit-status API method, a DB-backed status publisher that resolves the local project and MR into GitLab project/ref metadata, and wire the publisher into worker runtime so the scheduler emits a `running` status at claim time and a terminal status at completion.

**Tech Stack:** Go, sqlc-generated DB accessors, existing GitLab HTTP client, scheduler/gate runtime wiring, Go tests with `httptest` and shared MySQL test DB.

### Task 1: Lock the public API with tests

**Files:**
- Modify: `internal/gitlab/client_test.go`
- Modify: `internal/scheduler/service_test.go`
- Modify: `cmd/worker/runtime_test.go`

**Step 1: Write the failing test**

- Add a GitLab client test for `POST /projects/:id/statuses/:sha`.
- Add a scheduler/runtime test that expects one `running` publish before processing and one terminal publish after processing.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/gitlab ./internal/scheduler ./cmd/worker`

Expected: FAIL because commit status support and in-progress publishing are not implemented yet.

### Task 2: Implement GitLab commit status API support

**Files:**
- Modify: `internal/gitlab/discussions.go` or `internal/gitlab/client.go`

**Step 1: Write minimal implementation**

- Add a request type for commit statuses.
- Implement a client method that posts `state`, `name`, `description`, optional `ref`, and optional `target_url` to `POST /projects/:id/statuses/:sha`.

**Step 2: Run targeted tests**

Run: `go test ./internal/gitlab`

Expected: PASS.

### Task 3: Add a DB-backed GitLab status publisher

**Files:**
- Create: `internal/gate/gitlab_status_publisher.go`
- Modify: `internal/gate/service_test.go` if needed

**Step 1: Write minimal implementation**

- Resolve local `project_id` to `projects.gitlab_project_id`.
- Resolve `merge_request_id` to `source_branch` and `web_url`.
- Map internal states to GitLab commit status states with context `mreviewer/ai-review`.

**Step 2: Run targeted tests**

Run: `go test ./internal/gate`

Expected: PASS.

### Task 4: Wire running + terminal status publication into the scheduler/runtime

**Files:**
- Modify: `internal/scheduler/service.go`
- Modify: `cmd/worker/runtime.go`
- Modify: `cmd/worker/main.go`

**Step 1: Write minimal implementation**

- Add a scheduler-level status publisher used only for the in-progress signal.
- Publish `running` after the run is claimed and reloaded, but before processor execution.
- Keep final `success`/`failed` publication on the existing gate path.
- Construct the real GitLab-backed publisher in worker main/runtime.

**Step 2: Run targeted tests**

Run: `go test ./internal/scheduler ./cmd/worker`

Expected: PASS.

### Task 5: Verify the shipped behavior

**Files:**
- Modify: `README.md` if feature description needs an update

**Step 1: Run verification**

Run: `go test ./internal/gitlab ./internal/gate ./internal/scheduler ./cmd/worker`

Expected: PASS.

Run: `go test ./...`

Expected: PASS, or report unrelated failures with evidence.
