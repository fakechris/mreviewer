# Schema Harness Reference

Date: 2026-04-03

## Purpose

This document records:

- the external references that informed the schema harness work
- the internal implementation shipped in mreviewer
- the benchmark method used to compare routes

## External References

### AutoBe function-calling harness article

URL:

- https://autobe.dev/blog/function-calling-harness-qwen-meetup-korea/

Key ideas we adopted:

- first-pass function-calling success is a weak metric
- deterministic validation plus repair loops matter more than prompt wording
- schema design should remove invalid degrees of freedom
- parser leniency and validator strictness should be separate layers
- repair feedback should be precise and field-localized

### Kimi Wonder Verifier

Blog:

- https://www.kimi.com/blog/kimi-vendor-verifier

Source:

- https://github.com/MoonshotAI/K2-Vendor-Verifier

Relevant takeaway:

- `schema_accuracy` is measured by validating emitted tool/function arguments against the declared request schema

That directly motivated adding route-level schema benchmark metrics to mreviewer.

## Internal Implementation

### Shared schema harness

Main file:

- `internal/llm/schema_harness.go`

Responsibilities:

- detect initial structured output success or miss
- run strict schema validation
- issue structured repair payloads
- salvage parseable valid raw JSON where possible
- emit `SchemaExecutionReport`

### Structured validation issues

Main file:

- `internal/llm/parser.go`

Important additions:

- `validateReviewResultStrictIssues`
- `SchemaIssue`
- `validation_issues` in repair payloads

### Provider integration

Main files:

- `internal/llm/openai.go`
- `internal/llm/minimax.go`
- `internal/llm/provider.go`

Provider behavior now includes:

- shared harness execution
- `SchemaReport` on successful provider responses
- `SchemaReportFromError` for parser failures
- direct salvage of raw JSON on structured-output misses

### Benchmark command

Main file:

- `cmd/mreviewer/schema_benchmark.go`

Usage:

```bash
go run ./cmd/mreviewer schema-benchmark \
  --config config.yaml \
  --routes kimi_turbo,doubao_turbo,minimax_reasoning \
  --input testdata/schema-benchmark/review_requests.jsonl
```

Output metrics:

- `initial_schema_accuracy`
- `repair_rate`
- `final_success_rate`
- `failure_reasons`

## Route Notes

### Doubao

Primary tested route:

- provider: `ark_openai`
- base URL: `https://ark.cn-beijing.volces.com/api/coding/v3`
- model: `doubao-seed-2.0-code`

Observed behavior:

- can return valid schema JSON in plain assistant content
- does not always emit the expected tool call wrapper

Current mitigation:

- direct salvage of valid raw JSON from structured-output misses

### Kimi

Tested route:

- provider: `fireworks_router`
- base URL: `https://api.fireworks.ai/inference`
- model: `accounts/fireworks/routers/kimi-k2p5-turbo`

Observed behavior:

- strongest first-pass schema adherence among tested routes
- still benefits from keeping repair/reporting logic centralized

### MiniMax

Tested route:

- provider: `minimax`
- base URL: `https://api.minimaxi.com/anthropic`
- model: `MiniMax-M2.7-highspeed`

Observed behavior:

- lower first-pass adherence than Kimi
- still reaches full convergence under the harness

## Benchmark Fixture

Main file:

- `testdata/schema-benchmark/review_requests.jsonl`

Current fixture count:

- 10 requests

Current coverage:

- nil handling
- SQL injection
- loop bounds
- auth bypass
- path traversal
- transaction boundaries
- timeout removal
- concurrency hazards
- allocation abuse
- temporary file handling

## Work Completed

The following work was completed in this iteration:

- created a shared schema harness
- centralized strict validation reporting
- added structured repair issues
- added route-level schema benchmarking
- expanded the benchmark fixture from 3 to 10 requests
- validated Doubao, Kimi, and MiniMax with the same benchmark interface
- documented working route examples in `config.example.yaml`

## Validation

Validation performed on 2026-04-03:

- focused package tests for `internal/llm`
- benchmark runs against live provider routes
- full repository test suite via `go test ./...`
