# GLM-5 ZhipuAI Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add first-class `zhipuai` provider support so `mreviewer` can run GLM-5 against Zhipu's official OpenAI-compatible coding endpoint with a generated CLI config that works out of the box.

**Architecture:** Reuse the existing `OpenAIProvider` transport and parser, but introduce a Zhipu-specific compatibility mode so request payloads match Zhipu's documented constraints. Wire a new provider kind through the factory, expose a `mreviewer init --provider zhipuai` template, and update docs/examples so users can configure `GLM-5` without hand-editing internals.

**Tech Stack:** Go, stdlib `net/http`, existing `internal/llm` provider abstraction, CLI init/doctor flow, Go tests.

---

### Task 1: Add Zhipu payload compatibility

**Files:**
- Modify: `internal/llm/provider.go`
- Modify: `internal/llm/openai.go`
- Test: `internal/llm/provider_test.go`

**Step 1: Write the failing test**

Add payload-shape tests that assert a Zhipu route:
- uses `system` as the system message role
- omits `parallel_tool_calls`
- omits strict schema fields
- omits `reasoning_effort`
- uses `max_tokens`
- emits `tool_choice: "auto"` instead of named function forcing

**Step 2: Run test to verify it fails**

Run: `go test ./internal/llm -run 'TestOpenAIProviderZhipuAICompatMode|TestNewProviderFromConfigSupportsKnownKinds' -count=1`
Expected: FAIL because the provider does not yet support `zhipuai` behavior or `tool_choice: "auto"`.

**Step 3: Write minimal implementation**

Add:
- a `ToolChoiceMode` field to `OpenAICompatMode`
- a `ZhipuAICompatMode()` helper
- a `ProviderKindZhipuAI` constant
- a `NewZhipuAIProvider()` constructor that reuses `NewOpenAIProvider`
- factory wiring for `provider: zhipuai`
- request payload logic that emits string `"auto"` when compat mode requires it

**Step 4: Run test to verify it passes**

Run: `go test ./internal/llm -run 'TestOpenAIProviderZhipuAICompatMode|TestNewProviderFromConfigSupportsKnownKinds' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/llm/provider.go internal/llm/openai.go internal/llm/factory.go internal/llm/provider_test.go internal/llm/factory_test.go
git commit -m "feat: add zhipuai openai compat provider"
```

### Task 2: Add personal CLI init support

**Files:**
- Modify: `cmd/mreviewer/init_doctor.go`
- Modify: `cmd/mreviewer/init_doctor_test.go`
- Modify: `cmd/mreviewer/cli_common.go`

**Step 1: Write the failing test**

Add tests that assert:
- `renderPersonalConfig("zhipuai")` succeeds
- generated YAML uses `provider: zhipuai`
- generated model defaults to `glm-5`
- generated base URL is `https://open.bigmodel.cn/api/coding/paas/v4`
- generated API key env is `${ZHIPUAI_API_KEY}`

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/mreviewer -run 'TestRenderPersonalConfigZhipuAI|TestRunInitCommandWritesConfig' -count=1`
Expected: FAIL because `zhipuai` is not a supported init template yet.

**Step 3: Write minimal implementation**

Update:
- init flag help text to include `zhipuai`
- the provider template map in `renderPersonalConfig`
- CLI example text to mention `zhipuai` where useful

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/mreviewer -run 'TestRenderPersonalConfigZhipuAI|TestRunInitCommandWritesConfig|TestRunInitCommandDryRunPrintsConfigWithoutWriting' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add cmd/mreviewer/init_doctor.go cmd/mreviewer/init_doctor_test.go cmd/mreviewer/cli_common.go
git commit -m "feat: add zhipuai init template"
```

### Task 3: Update sample config and docs

**Files:**
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `README.md`
- Modify: `README.zh-CN.md`

**Step 1: Write the failing test**

Prefer doc/config regression tests where practical:
- extend example/config tests if needed
- otherwise use focused string assertions in existing Go tests for config examples

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run 'TestRepositoryConfigYAMLUsesModelCatalogSchema' -count=1`
Expected: PASS for schema, but any new example assertions added for Zhipu should FAIL first.

**Step 3: Write minimal implementation**

Document:
- `ZHIPUAI_API_KEY`
- `provider: zhipuai`
- GLM-5 example usage
- when to prefer `zhipuai` over generic `openai`

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config ./cmd/mreviewer -run 'TestRepositoryConfigYAMLUsesModelCatalogSchema|TestRenderPersonalConfigZhipuAI' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add .env.example config.example.yaml README.md README.zh-CN.md
git commit -m "docs: document glm-5 zhipuai setup"
```

### Task 4: Final verification

**Files:**
- Modify: none
- Test: `internal/llm/provider_test.go`
- Test: `internal/llm/factory_test.go`
- Test: `cmd/mreviewer/init_doctor_test.go`
- Test: `internal/config/model_catalog_test.go`

**Step 1: Run targeted verification suite**

Run:
- `go test ./internal/llm -count=1`
- `go test ./cmd/mreviewer -run 'TestRenderPersonalConfigZhipuAI|TestRunInitCommand|TestRunDoctorCommand' -count=1`
- `go test ./internal/config -count=1`

Expected: PASS

**Step 2: Run a full repo smoke test within local constraints**

Run: `go test ./...`
Expected: existing Docker-dependent packages may still fail with `rootless Docker not found`; non-Docker packages should remain green.

**Step 3: Summarize residual risk**

Call out that:
- live API verification against Zhipu was not exercised without a real key in this session
- full-repo integration tests remain blocked by local Docker availability, not by this change set
