# Schema Harness Benchmark Conclusions

Date: 2026-04-03

## Executive Summary

We should treat schema-constrained review output as a first-class subsystem, not a prompt detail.

The main conclusion from the AutoBe and Kimi verifier references held up in our own benchmarks:

- first-pass tool calling is not the right success metric
- reliability comes from a deterministic harness around the model
- the harness must own parse, validate, repair, and reporting

For mreviewer, that means the stable unit is now:

`model output -> lenient intake -> strict schema validation -> structured repair feedback -> retry/salvage -> schema report`

## What We Implemented

We added a shared schema harness for review output and routed both OpenAI-style and Anthropic-style providers through it.

Key properties:

- strict validation is centralized instead of duplicated per provider
- repair payloads now include structured `validation_issues`, not just a single error string
- provider responses now carry `SchemaReport`
- schema benchmarking is exposed as `mreviewer schema-benchmark`
- route examples for Doubao and Kimi are documented in `config.example.yaml`

## Benchmark Results

### 10-request benchmark set

Input file:

- `testdata/schema-benchmark/review_requests.jsonl`

Routes tested on 2026-04-03:

- `kimi_turbo` via Fireworks router
- `doubao_turbo` via Ark OpenAI-compatible coding endpoint
- `minimax_reasoning`

Results:

| Route | Model | Initial Schema Accuracy | Repair Rate | Final Success Rate |
| --- | --- | ---: | ---: | ---: |
| `kimi_turbo` | `accounts/fireworks/routers/kimi-k2p5-turbo` | 0.9 | 0.1 | 1.0 |
| `minimax_reasoning` | `MiniMax-M2.7-highspeed` | 0.7 | 0.3 | 1.0 |
| `doubao_turbo` | `doubao-seed-2.0-code` | 0.8 | 0.0 | 0.8 |

## Decisions

### 1. Keep strict schema validation

The evidence does not support relaxing schema validation. The failures were mostly harness failures or vendor transport/format variance, not evidence that strict validation itself is the wrong contract.

### 2. Prefer repairable harness metrics over first-pass metrics

The primary production metric should be final success under the harness, not first-pass tool-call success.

Recommended metrics:

- `initial_schema_accuracy`
- `repair_rate`
- `final_success_rate`
- top `failure_reasons`

### 3. Doubao should use Ark OpenAI-compatible coding route as the primary path

The usable Doubao route for current testing is:

- provider: `ark_openai`
- base URL: `https://ark.cn-beijing.volces.com/api/coding/v3`
- model: `doubao-seed-2.0-code`

We also confirmed that Doubao behavior is not identical to a clean OpenAI tool-call implementation. It can return valid JSON in assistant content without emitting a tool call. We added direct salvage for that path, which improved the 10-request run from:

- `0.7 -> 0.8` initial/final success
- `3 -> 2` missing-tool-use failures

### 4. Kimi and MiniMax are currently better schema-harness fits than Doubao

Both Kimi and MiniMax reached `final_success_rate=1.0` on the 10-request benchmark. Kimi had the highest first-pass schema accuracy. MiniMax required the most repair but still converged.

## Practical Takeaways

- `strict` should stay
- schema repair should be machine-readable
- benchmarking should stay route-based, not model-name-only
- small and weaker routes are useful harness probes, not just fallback options

## Next Recommendation

The next useful step is not more prompt tuning. It is collecting the remaining raw Doubao misses and classifying them into:

- valid JSON in plain content
- malformed JSON in plain content
- prose-only misses
- partial tool call emission

That will tell us whether the next gain comes from more salvage logic or a separate Doubao-specific repair policy.
