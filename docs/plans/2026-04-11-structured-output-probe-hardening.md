# Structured Output Probe Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a production-usable `structured-output-probe` command to `mreviewer`, document provider-specific guidance, and attach live acceptance evidence to the same PR.

**Architecture:** Keep the runtime default on `tool_call`, but add a route-level probe command that can exercise either synthetic-tool or provider-native JSON-schema contracts against configured routes. Surface risky `json_schema` strategies in `doctor`, and record live probe results in repo-tracked acceptance docs so operational guidance stays tied to evidence.

**Tech Stack:** Go CLI, YAML config loading, stdlib HTTP client, repo-tracked Markdown docs.

### Task 1: Lock the CLI surface with tests

**Files:**
- Modify: `cmd/mreviewer/structured_output_probe_test.go`
- Modify: `cmd/mreviewer/init_doctor_test.go`

**Step 1: Write the failing tests**

- Cover `structured-output-probe --help`.
- Cover OpenAI-compatible `tool` mode.
- Cover OpenAI-compatible `native` mode with fenced JSON.
- Cover a long native response body so parsing uses the full body, not a truncated preview.
- Cover Anthropic-compatible `tool` mode.
- Cover `doctor --json` warning for OpenAI-compatible `json_schema` routes.

**Step 2: Run the focused test set and confirm failure**

Run:

```bash
go test ./cmd/mreviewer -run 'TestRunCLIStructuredOutputProbeSubcommand|TestRunStructuredOutputProbeCommandOpenAITool|TestRunStructuredOutputProbeCommandOpenAINativeFencedJSON|TestRunStructuredOutputProbeCommandOpenAINativeParsesFullBodyNotPreview|TestRunStructuredOutputProbeCommandAnthropicTool|TestRunStructuredOutputProbeCommandRejectsAnthropicNativeMode|TestRunDoctorCommandWarnsForOpenAICompatibleJSONSchemaRoutes' -count=1
```

Expected: at least one failure before implementation is finalized.

### Task 2: Implement the probe and strategy warning

**Files:**
- Modify: `cmd/mreviewer/main.go`
- Modify: `cmd/mreviewer/cli_common.go`
- Modify: `cmd/mreviewer/init_doctor.go`
- Create: `cmd/mreviewer/structured_output_probe.go`

**Step 1: Expose the subcommand**

- Register `structured-output-probe` in top-level CLI routing and help output.

**Step 2: Implement minimal live probe logic**

- Load route config from YAML.
- Infer the wire protocol from provider kind.
- For OpenAI-compatible routes:
  - `tool` mode uses a synthetic `StructuredOutput` function tool.
  - `native` mode uses `response_format.type=json_schema`.
- For Anthropic-compatible routes:
  - support only `tool` mode.
- Emit JSON summary with HTTP status, parse result, schema result, text preview, reasoning preview, and observed model.

**Step 3: Add doctor guidance**

- Detect non-first-party OpenAI-compatible routes with `output_mode=json_schema`.
- Emit a warning that local validation is required and `tool_call` remains the safer default.

### Task 3: Add operator-facing documentation and acceptance evidence

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Create: `docs/acceptance/2026-04-11-structured-output-probe-matrix.md`

**Step 1: Document how to run the probe**

- Show the command shape.
- State when to use it: before promoting a provider-native `json_schema` route to production default.

**Step 2: Record live acceptance evidence**

- Run the new CLI against GLM-5 and MiniMax with temporary config and environment variables only.
- Record command shapes, route matrix, observed success/failure rates, and the production recommendation.

### Task 4: Verify end to end before commit

**Files:**
- Modify: tracked files from Tasks 1-3 only

**Step 1: Format**

Run:

```bash
gofmt -w cmd/mreviewer/main.go cmd/mreviewer/cli_common.go cmd/mreviewer/init_doctor.go cmd/mreviewer/init_doctor_test.go cmd/mreviewer/structured_output_probe.go cmd/mreviewer/structured_output_probe_test.go
```

**Step 2: Run focused tests**

Run:

```bash
go test ./cmd/mreviewer -run 'TestRunCLIStructuredOutputProbeSubcommand|TestRunStructuredOutputProbeCommandOpenAITool|TestRunStructuredOutputProbeCommandOpenAINativeFencedJSON|TestRunStructuredOutputProbeCommandOpenAINativeParsesFullBodyNotPreview|TestRunStructuredOutputProbeCommandAnthropicTool|TestRunStructuredOutputProbeCommandRejectsAnthropicNativeMode|TestRunDoctorCommandWarnsForOpenAICompatibleJSONSchemaRoutes|TestRunInitCommandWritesConfig|TestRunInitCommandDryRunPrintsConfigWithoutWriting' -count=1
```

**Step 3: Run broader verification**

Run:

```bash
go test ./cmd/worker -run 'TestProviderConfigsFromModelChainConfig|TestValidateWorkerConfigAllowsConfiguredToken' -count=1
go test ./internal/config -run 'TestConfigExpandsEnvVarsInsideYAML|TestRepositoryConfigYAMLUsesModelCatalogSchema|TestResolveModelChainBuildsProviderConfigsFromCatalog' -count=1
bash scripts/verify-onboarding.sh
```

**Step 4: Check secrets and commit scope**

Run:

```bash
git grep -n '20e3533e4511498291df2a796ba5b070\\.ljNntfsBrQcwTAQS' -- . ':!docs/acceptance'
git status --short
```

Expected: no tracked file contains the live key; only intended files are staged.
