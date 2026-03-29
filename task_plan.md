# Task Plan

## Goal

Implement the review remediation work in this order:

1. correctness fixes
3. security hardening
2. performance fixes
4. architecture cleanup

## Phases

| Phase | Status | Scope |
| --- | --- | --- |
| 1 | completed | Gate reads structured discussion resolution state; findings store structured range anchors instead of parsing `evidence` text |
| 3 | completed | Prompt-injection handling upgraded from brittle keyword matching to explicit untrusted-content policy plus broader detection patterns |
| 2 | completed | Writeback uses bounded concurrency and scheduler runs multiple worker loops inside one process |
| 4 | in_progress | Duplicate DB helpers removed, GitLab discussion types decoupled from writer, command typing tightened; `internal/llm/provider.go` split still pending |

## Verification Targets

- `go test ./internal/gate ./internal/writer ./internal/llm`
- `go test ./internal/scheduler ./cmd/worker`
- targeted regression tests for new schema and writeback behavior

## Risks

- schema changes in `review_findings` can break writer and persistence paths
- gate logic currently derives state from finding text, so migration must preserve existing behavior where possible
- writeback concurrency must remain idempotent and respect GitLab API retry logic
