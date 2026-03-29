# Multi-Provider Tool-Call Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace MiniMax free-text JSON review output with tool-call output, and evolve the current route registry into a real multi-provider factory supporting MiniMax, OpenAI, and Anthropic routes.

**Architecture:** Keep `internal/llm.Provider` and `ProviderRegistry` as the runtime abstraction, but move provider construction into a route factory driven by neutral route config. MiniMax M2.7 will use Anthropic-compatible tool calls with a single forced `submit_review` tool and local schema validation. OpenAI and Anthropic routes will plug into the same registry through provider-specific adapters using each vendor's strongest structured-output path. Invalid structured output will go through validator, one repair retry, then safe degradation.

**Tech Stack:** Go, Anthropic Go SDK, existing scheduler/processor pipeline, existing rules `provider_route`, JSON schema-style validation in Go.

### Task 1: Lock route config shape

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`
- Modify: `README.md`
- Test: `internal/config/config_test.go`

**Step 1: Write failing config tests**

Add tests for:
- multiple named LLM routes
- default/fallback route selection
- MiniMax route loaded from env/yaml
- provider kind parsing

**Step 2: Implement neutral config**

Add route config supporting:
- `provider`
- `base_url`
- `api_key`
- `model`
- `output_mode`
- `temperature`

Preserve current MiniMax env fallback for compatibility.

**Step 3: Verify tests**

Run: `go test ./internal/config`

### Task 2: Add provider factory

**Files:**
- Modify: `internal/llm/provider.go`
- Create: `internal/llm/factory.go`
- Test: `internal/llm/provider_test.go`

**Step 1: Write failing factory tests**

Cover:
- building MiniMax route
- building OpenAI route
- building Anthropic route
- unknown provider kind rejected

**Step 2: Implement factory**

Create route-factory functions that return `Provider` instances and register them into `ProviderRegistry`.

**Step 3: Verify tests**

Run: `go test ./internal/llm -run 'TestProviderFactory|TestProviderRouteSelection'`

### Task 3: Add tool-call review mode for MiniMax

**Files:**
- Modify: `internal/llm/provider.go`
- Test: `internal/llm/provider_test.go`

**Step 1: Write failing provider tests**

Cover:
- request payload contains `tools`
- request payload contains forced `tool_choice`
- response parses tool arguments instead of assistant text
- parser error path when no tool call returned

**Step 2: Implement tool-call request/response handling**

For MiniMax M2.7:
- define `submit_review`
- force `tool_choice`
- parse tool input JSON from `tool_use`
- keep current text fallback only as explicit compatibility path, not default

**Step 3: Verify tests**

Run: `go test ./internal/llm -run 'TestMiniMaxToolCall|TestMiniMaxRequestShape|TestMiniMaxParserErrorIncludesRawSnippet'`

### Task 4: Add validator + repair retry + safe degrade

**Files:**
- Modify: `internal/llm/provider.go`
- Test: `internal/llm/provider_test.go`
- Test: `cmd/worker/runtime_test.go`

**Step 1: Write failing tests**

Cover:
- missing required field rejected
- unsupported extra fields rejected in strict mode
- one repair retry issued with validation errors
- persistent invalid output returns parser/safe-degrade status, never “no findings”

**Step 2: Implement validation flow**

Validate canonical review result after parse. If invalid:
- build repair prompt containing only validation errors and the invalid payload
- retry once
- if still invalid, return structured parser failure

**Step 3: Verify tests**

Run: `go test ./internal/llm ./cmd/worker -run 'Test.*Repair|Test.*InvalidStructuredOutput|TestWorkerRuntimeAllows'`

### Task 5: Wire multi-provider routes into worker

**Files:**
- Modify: `cmd/worker/main.go`
- Modify: `README.md`

**Step 1: Build registry from config**

Register named routes from config instead of hard-coded `default` and `secondary` MiniMax only.

**Step 2: Keep policy route selection**

Continue using `rules.EffectivePolicy.ProviderRoute`, but now allow:
- `minimax`
- `openai`
- `anthropic`

**Step 3: Verify**

Run: `go test ./cmd/worker`

### Task 6: Document migration and examples

**Files:**
- Modify: `README.md`
- Modify: `.env.example`
- Modify: `config.yaml`

**Step 1: Add provider examples**

Show:
- MiniMax M2.7 route
- OpenAI route
- Anthropic route

**Step 2: Explain structured-output strategy**

Document:
- MiniMax uses tool call, not free-text JSON
- OpenAI route should use strict schema path
- Anthropic route uses tool-use path plus validator/retry

**Step 3: Verify docs**

Run: `rg -n 'provider_route|tool call|MiniMax|OpenAI|Anthropic' README.md config.yaml .env.example`

### Task 7: Final verification

**Files:**
- No code changes

**Step 1: Run targeted tests**

Run:
- `go test ./internal/config`
- `go test ./internal/llm`
- `go test ./cmd/worker`

**Step 2: Run full regression**

Run: `go test ./...`

**Step 3: Commit**

Commit after tests pass with a single feature commit or small logical commits.
