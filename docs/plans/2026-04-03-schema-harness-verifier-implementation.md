# Schema Harness Verifier Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Turn the review structured-output schema into a first-class reusable harness, then add Wonder Verifier-style schema-accuracy benchmarking for provider routes such as Kimi Turbo, Doubao, and MiniMax.

**Architecture:** Move the current review-output validation, repair, salvage, and reporting flow out of provider-specific code into a shared `internal/llm` schema harness. Providers should only fetch raw structured output and delegate all schema processing to the harness. Add a benchmark command that runs serialized `ReviewRequest` fixtures through configured routes and reports first-pass schema accuracy, repair rate, final success rate, and failure reasons.

**Tech Stack:** Go, existing `internal/llm` provider abstractions, existing config/model route registry, current review JSON schema validator, JSONL fixtures, targeted `go test` plus optional live route checks.

### Task 1: Introduce first-class schema harness types

**Files:**
- Create: `internal/llm/schema_harness.go`
- Test: `internal/llm/schema_harness_test.go`
- Modify: `internal/llm/provider.go`

**Step 1: Write failing harness tests**

Cover:
- valid first-pass raw JSON returns `initial_valid=true`
- strict validation failure records machine-localized issues
- missing structured output with recoverable raw text is marked repairable
- repair success records `repair_attempted=true` and `final_valid=true`
- repair failure preserves validation issue list

**Step 2: Add harness types**

Define:
- `SchemaIssue`
- `SchemaAttemptReport`
- `SchemaExecutionReport`
- `ReviewSchemaHarness`
- `StructuredOutputCandidate`

Expose helpers for:
- strict validation with parsed issue objects
- repair payload construction
- parse/normalize/salvage execution
- benchmark-friendly summary fields

**Step 3: Verify tests**

Run: `go test ./internal/llm -run 'TestReviewSchemaHarness|TestStrictValidation'`

### Task 2: Move provider repair logic into the shared harness

**Files:**
- Modify: `internal/llm/openai.go`
- Modify: `internal/llm/minimax.go`
- Modify: `internal/llm/provider_test.go`

**Step 1: Write failing provider tests**

Cover:
- OpenAI provider can repair from recoverable plain-text/malformed structured output
- MiniMax provider still repairs invalid tool input
- provider responses include a populated `SchemaExecutionReport`
- fallback stage remains compatible with existing expectations

**Step 2: Integrate shared harness**

Refactor both providers so they:
- obtain an initial structured-output candidate
- invoke the shared schema harness
- return the resulting parsed `ReviewResult`
- attach `SchemaExecutionReport` to `ProviderResponse`

Keep provider-specific transport, timeout, and auth logic unchanged.

**Step 3: Verify tests**

Run: `go test ./internal/llm -run 'TestMiniMaxToolCall|TestOpenAIProvider|Test.*Schema.*'`

### Task 3: Add structured schema issue parsing and reusable reporting

**Files:**
- Modify: `internal/llm/parser.go`
- Modify: `internal/llm/provider.go`
- Test: `internal/llm/schema_harness_test.go`

**Step 1: Write failing validation-report tests**

Cover:
- required-field failures become issues with exact JSON paths
- additional-property failures are preserved
- decode failures are represented as structured issues

**Step 2: Implement structured issue extraction**

Replace string-only validation handling with:
- `[]SchemaIssue` alongside the human-readable error string
- helpers that keep exact path and message for repair feedback and benchmarking

Do not remove the existing human-readable error text used in logs.

**Step 3: Verify tests**

Run: `go test ./internal/llm -run 'TestStrictValidationIssues|TestReviewSchemaHarness'`

### Task 4: Add Wonder Verifier-style schema benchmark command

**Files:**
- Create: `cmd/mreviewer/schema_benchmark.go`
- Create: `cmd/mreviewer/schema_benchmark_test.go`
- Modify: `cmd/mreviewer/main.go`
- Modify: `cmd/mreviewer/main_test.go`
- Create: `testdata/schema-benchmark/review_requests.jsonl`

**Step 1: Write failing CLI tests**

Cover:
- `mreviewer schema-benchmark` subcommand dispatch
- route selection from config
- JSON summary output includes:
  - `initial_schema_accuracy`
  - `repair_rate`
  - `final_success_rate`
  - `failure_reasons`

**Step 2: Implement benchmark command**

Command behavior:
- load config and resolve one or more route names
- load JSONL `ReviewRequest` fixtures
- call each provider route against each fixture
- aggregate `SchemaExecutionReport` into Wonder Verifier-style metrics
- print machine-readable JSON summary

**Step 3: Verify tests**

Run: `go test ./cmd/mreviewer -run 'TestSchemaBenchmark|TestRunCLI'`

### Task 5: Add route fixtures and docs for live provider comparison

**Files:**
- Modify: `config.example.yaml`
- Modify: `README.md`
- Modify: `README.zh-CN.md`

**Step 1: Add route examples**

Document sample routes for:
- MiniMax
- Doubao via `ark_openai`
- Kimi Turbo via `fireworks_router` or another configured Kimi-compatible route

**Step 2: Add benchmark usage docs**

Document how to run:

```bash
mreviewer schema-benchmark \
  --config config.yaml \
  --routes minimax_reasoning,doubao_turbo,kimi_turbo \
  --input testdata/schema-benchmark/review_requests.jsonl
```

Explain the key metrics and how they map to harness quality.

**Step 3: Verify docs**

Run: `rg -n 'schema-benchmark|initial_schema_accuracy|repair_rate|doubao|kimi|MiniMax' README.md README.zh-CN.md config.example.yaml`

### Task 6: Run verification and live route checks

**Files:**
- No code changes

**Step 1: Run targeted tests**

Run:
- `go test ./internal/llm`
- `go test ./cmd/mreviewer`

**Step 2: Run full regression**

Run:
- `go test ./...`

**Step 3: Run live schema benchmark if credentials are present**

Run:

```bash
go run ./cmd/mreviewer schema-benchmark \
  --config config.yaml \
  --routes minimax_reasoning,doubao_turbo,kimi_turbo \
  --input testdata/schema-benchmark/review_requests.jsonl
```

Record:
- per-route initial schema accuracy
- repair rate
- final success rate
- top failure reasons

**Step 4: Commit**

Commit after tests pass and benchmark output is captured or explicitly blocked by missing credentials.
