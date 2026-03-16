---
name: go-backend
description: Build Go domain, config, rules, context, and finding-engine features with test-first discipline.
---

# go-backend

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the work procedure.

## When to Use This Skill

Use for Go backend features centered on internal logic rather than external API handshakes:

- configuration loading
- health/runtime behavior
- rules loading and config precedence
- diff/context assembly
- finding normalization, fingerprinting, state transitions
- pure package refactors and domain logic

## Work Procedure

1. Read `mission.md`, `AGENTS.md`, `.factory/library/*.md`, and the assigned feature carefully.
2. Inspect nearby code and existing package patterns before changing anything.
3. Write failing tests first for the feature's expected behavior. Prefer table-driven tests.
4. Implement the smallest code change needed to pass those tests.
5. Run `gofmt` on every touched Go file.
6. Run targeted tests for touched packages first, then `go test -run '^$' ./...`, then `go build ./...`.
7. If the feature affects API-visible behavior indirectly, add or update integration coverage to prove it from the next layer up.
8. Record exact commands, observations, and any remaining gaps in the handoff.

## Example Handoff

```json
{
  "salientSummary": "Implemented config precedence and root REVIEW.md loading with explicit trusted-source filtering. Added table-driven tests for env-over-yaml behavior and for ignoring non-allowlisted files as instructions.",
  "whatWasImplemented": "Added config loader support for environment-over-YAML precedence, root REVIEW.md fetching, and trusted instruction filtering so only REVIEW.md and .gitlab/ai-review.yaml influence effective rules. Also added deterministic rules digest generation and prompt-injection isolation tests.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      {
        "command": "go test ./internal/config ./internal/rules -run 'TestConfigPrecedence|TestRootReviewLoad|TestAllowlistedRuleSourcesOnly'",
        "exitCode": 0,
        "observation": "All targeted tests passed; table-driven coverage exercises both success and fallback paths."
      },
      {
        "command": "gofmt -w internal/config/config.go internal/config/config_test.go internal/rules/loader.go internal/rules/loader_test.go",
        "exitCode": 0,
        "observation": "Formatting applied cleanly."
      },
      {
        "command": "go test -run '^$' ./...",
        "exitCode": 0,
        "observation": "All packages compile after the change."
      },
      {
        "command": "go build ./...",
        "exitCode": 0,
        "observation": "Full build succeeded."
      }
    ],
    "interactiveChecks": [
      {
        "action": "Reviewed captured provider-request fixture for a malicious README injection string.",
        "observed": "System prompt remained unchanged and the malicious text appeared only in untrusted context fields."
      }
    ]
  },
  "tests": {
    "added": [
      {
        "file": "internal/rules/loader_test.go",
        "cases": [
          {
            "name": "TestRootReviewLoad",
            "verifies": "root REVIEW.md is loaded when present"
          },
          {
            "name": "TestAllowlistedRuleSourcesOnly",
            "verifies": "non-allowlisted repository files cannot become trusted instructions"
          }
        ]
      }
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- The feature requires a missing upstream schema or interface that belongs to another pending feature.
- The feature depends on live GitLab/provider credentials to make progress beyond mocked validation.
- The assigned feature actually needs heavy external integration behavior better suited to `go-integration`.
- You discover a contract or milestone inconsistency that changes which assertions this feature should fulfill.
