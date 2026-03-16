---
name: go-integration
description: Build Go features that integrate with HTTP surfaces, GitLab APIs, provider adapters, and writeback flows.
---

# go-integration

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the work procedure.

## When to Use This Skill

Use for features that touch real interfaces and contract boundaries:

- webhook handlers
- scheduler entrypoints when driven by HTTP-triggered runs
- GitLab API adapters
- LLM/provider adapters
- comment writer and merge-gate outputs
- command webhooks and control-plane note handling

## Work Procedure

1. Read the assigned feature plus relevant API contracts and `.factory/library/integrations.md`.
2. Identify the exact external contract to satisfy (request shape, response shape, retry behavior, auth rules).
3. Write failing adapter/handler tests first using `httptest.Server` or request fixtures.
4. Implement the integration in thin adapters; keep parsing/mapping logic isolated for unit testing.
5. Add at least one higher-level integration test proving the feature from its public entrypoint.
6. If the feature exposes a local HTTP route, verify it manually with `curl` and capture the response.
7. Run `gofmt`, targeted tests, then `go test -run '^$' ./...`, then `go build ./...`.
8. Record exact payload shapes, retry observations, and any mocked-vs-live limitations in the handoff.

## Example Handoff

```json
{
  "salientSummary": "Implemented GitLab versions/diffs readers plus retry handling for diff-not-ready and 429 responses. Verified the adapter against paginated fixtures and a local curl-based webhook path.",
  "whatWasImplemented": "Added GitLab client methods for MR versions and paginated diffs, wired retry/backoff for diff-not-ready and 429 responses, and exposed the webhook handler path that creates pending review runs from valid MR open payloads. Tests cover pagination, version SHA extraction, token verification, and malformed payload rejection.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      {
        "command": "go test ./internal/gitlab ./internal/hooks -run 'TestGetMergeRequestVersions|TestGetMergeRequestDiffsPagination|TestDiffNotReadyRetry|TestWebhookAuth'",
        "exitCode": 0,
        "observation": "All targeted contract tests passed, including retry scenarios."
      },
      {
        "command": "gofmt -w internal/gitlab/client.go internal/gitlab/client_test.go internal/hooks/handler.go internal/hooks/handler_test.go",
        "exitCode": 0,
        "observation": "Formatting applied without diffs afterward."
      },
      {
        "command": "go test -run '^$' ./...",
        "exitCode": 0,
        "observation": "Full compile check succeeded."
      },
      {
        "command": "go build ./...",
        "exitCode": 0,
        "observation": "Binary build succeeded."
      }
    ],
    "interactiveChecks": [
      {
        "action": "Manual: POSTed a valid MR-open webhook fixture with curl to /webhook.",
        "observed": "Received HTTP 200 and observed one pending review_run row in the database."
      }
    ]
  },
  "tests": {
    "added": [
      {
        "file": "internal/gitlab/client_test.go",
        "cases": [
          {
            "name": "TestGetMergeRequestDiffsPagination",
            "verifies": "diff pages are aggregated across the full MR"
          },
          {
            "name": "TestDiffNotReadyRetry",
            "verifies": "the client retries when versions/diff refs are temporarily unavailable"
          }
        ]
      },
      {
        "file": "internal/hooks/handler_test.go",
        "cases": [
          {
            "name": "TestWebhookAuth",
            "verifies": "only requests with the configured X-Gitlab-Token are accepted"
          }
        ]
      }
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- Live integration credentials or instance details are required and unavailable.
- The feature needs a foundational DB/schema/concurrency primitive that is not implemented yet.
- GitLab/provider contract ambiguity changes the scope or validation contract materially.
- The work turns into a reliability/concurrency problem better suited to `go-reliability`.
