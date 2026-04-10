# Task Plan

## Goal
Run live GLM-5 provider checks against Zhipu's official endpoint, with emphasis on structured output reliability under `tool_call` and `json_schema`.

## Phases
- [completed] Inspect existing benchmark/live-test entry points
- [completed] Prepare benchmark inputs and isolated config
- [completed] Run live GLM-5 benchmark(s) and direct probes
- [completed] Analyze strict-schema behavior and summarize evidence

## Errors Encountered
- `go test ./...` and some package-wide test runs fail in this machine because Docker-backed `testcontainers` suites cannot start: `rootless Docker not found`

## Output
- Direct probe conclusion: `tool_call + auto` is usable on Zhipu's coding endpoint, but `json_schema strict=true` is not reliable enough to be the primary path.
- Benchmark artifact: `/tmp/glm51_benchmark_2026-04-10.json` (local-only, not checked in)
- Product-facing evidence doc: `docs/acceptance/2026-04-10-zhipu-glm51-structured-output-probe.md`
