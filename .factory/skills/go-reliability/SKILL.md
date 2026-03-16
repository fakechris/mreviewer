---
name: go-reliability
description: Build Go features focused on transactions, retries, concurrency control, observability, degradation, and recovery.
---

# go-reliability

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the work procedure.

## When to Use This Skill

Use for features where correctness under failure matters most:

- migrations and persistence primitives
- scheduler claiming and retries
- idempotency and recovery logic
- metrics, traces, and audit completeness
- rate limiting and degraded-mode behavior
- large-MR degradation and benchmark harnesses

## Work Procedure

1. Read the feature, `AGENTS.md`, and the validation assertions it fulfills.
2. Identify the failure modes first: race conditions, duplicate writes, partial transactions, timeouts, unavailable dependencies, or degraded backends.
3. Write failing tests first for those failure modes. Include concurrency/fault-injection tests where relevant.
4. Implement the minimum safe mechanism (transactions, unique constraints, backoff, locks, fallbacks, metrics/tracing hooks).
5. Verify that the happy path still works after the failure handling is added.
6. Run `gofmt`, targeted tests, broader compile checks, and any package-specific benchmark or concurrency tests required by the feature.
7. Explicitly report what was simulated, what recovered, and what remains dependent on later features.

## Example Handoff

```json
{
  "salientSummary": "Implemented scheduler claim safety and idempotent comment-action recovery. Added concurrent-claim tests plus a restart-after-partial-batch scenario to prove one-claim-per-run and no duplicate discussions.",
  "whatWasImplemented": "Added transactional run claiming with unique ownership, bounded exponential retry metadata, deterministic comment_action idempotency keys, and restart-safe recovery that skips already-succeeded write operations. Also instrumented run metrics and machine-readable failure codes for parser/writer failures.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      {
        "command": "go test ./internal/scheduler ./internal/writer -run 'TestSingleClaimAcrossWorkers|TestRetryBackoff|TestCommentIdempotency|TestResumeAfterPartialBatch|TestFailureReasonCodes'",
        "exitCode": 0,
        "observation": "Concurrency and recovery tests passed with no duplicate claims or duplicate writes."
      },
      {
        "command": "gofmt -w internal/scheduler/claim.go internal/scheduler/claim_test.go internal/writer/actions.go internal/writer/actions_test.go",
        "exitCode": 0,
        "observation": "Formatting complete."
      },
      {
        "command": "go test -run '^$' ./...",
        "exitCode": 0,
        "observation": "Repository compiles after reliability changes."
      },
      {
        "command": "go build ./...",
        "exitCode": 0,
        "observation": "Full build succeeded."
      }
    ],
    "interactiveChecks": [
      {
        "action": "Manual: inspected the latest audit_logs and comment_actions rows after a forced retry scenario.",
        "observed": "Saw one successful action per idempotency key and machine-readable error codes on failed attempts before recovery."
      }
    ]
  },
  "tests": {
    "added": [
      {
        "file": "internal/scheduler/claim_test.go",
        "cases": [
          {
            "name": "TestSingleClaimAcrossWorkers",
            "verifies": "two workers cannot claim the same review_run simultaneously"
          },
          {
            "name": "TestRetryBackoff",
            "verifies": "retry metadata and next-attempt scheduling follow bounded backoff rules"
          }
        ]
      },
      {
        "file": "internal/writer/actions_test.go",
        "cases": [
          {
            "name": "TestCommentIdempotency",
            "verifies": "replayed write operations do not create duplicate comment actions or discussions"
          },
          {
            "name": "TestResumeAfterPartialBatch",
            "verifies": "writer recovery skips already successful actions after a crash"
          }
        ]
      }
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- The feature requires a product decision about acceptable degraded behavior or merge-gate semantics.
- A required dependency failure cannot be simulated or recovered locally.
- The feature reveals a contract gap around retries, idempotency ownership, or performance targets.
- The work depends on unreleased live infrastructure rather than the approved mock-first validation path.
