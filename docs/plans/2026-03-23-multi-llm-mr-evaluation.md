# Multi-LLM MR Evaluation

## Scope

This document records the regression and acceptance run completed on 2026-03-23 for the multi-LLM routing branch.

Validated models:

- `minimax-review`
- `claude-opus-4-6`
- `openai-gpt-5-4`

Validated merge requests in `songchuansheng/case-revenue-bridge`:

- `MR !7`
- `MR !8`
- `MR !9`

## This Round's Product Fix

The branch-level product fix in this round was not an MR business-logic fix inside `case-revenue-bridge`. It was a reviewer compatibility fix in this repository so Claude Opus could complete strict structured review output reliably.

Implemented change:

- Keep the full review schema for MiniMax.
- Use a compact `submit_review` finding schema for Anthropic-compatible providers.
- Preserve root-level `summary_note` and `blind_spots` for Anthropic.
- Add a regression test to lock the compact Anthropic request shape.

Files changed:

- `internal/llm/parser.go`
- `internal/llm/minimax.go`
- `internal/llm/provider_test.go`

Why this was needed:

- Simple Anthropic-compatible requests succeeded.
- Full strict review output with the original finding schema timed out or ended with remote EOF.
- After narrowing the Anthropic finding item schema, Claude Opus completed both smoke tests and full MR review runs.

## Verified Bugs By MR

### MR !7

Files:

- `src/lib/arrayHelper.ts`
- `src/lib/dataValidator.ts`

Core bugs confirmed from source:

1. `unique` uses `i <= arr.length`, reads `arr[arr.length]`, and may append `undefined`.
2. `chunk` uses `i <= arr.length`, producing an extra empty chunk at the tail.
3. `flatten` uses `i <= arr.length`, pushing an extra `undefined`.
4. `groupBy` uses `i <= arr.length`, then dereferences `item[key]` when `item` is `undefined`.
5. `validateRecord` uses `i <= rules.length`, then dereferences `rule.field` when `rule` is `undefined`.
6. `mergeRecords` uses `i <= records.length`, then executes `for...in` on `undefined`.

Extra edge-case findings reported by some models:

- `flatten` only flattens one level, which may violate caller expectations.
- `chunk` does not guard `size <= 0`.

### MR !8

File:

- `src/lib/mathUtils.ts`

Core bugs confirmed from source:

1. `average` uses `i <= arr.length`, adds `undefined`, and can return `NaN`.
2. `percentile` uses an invalid index formula and can read past the sorted array.

Extra edge-case finding reported by some models:

- `median([])` returns `undefined` instead of a stable numeric result or explicit error handling.

### MR !9

File:

- `src/lib/objectHelper.ts`

Core bugs confirmed from source:

1. `pick` uses `i <= keys.length`, causing one extra iteration.
2. `omit` uses `i <= keys.length`, causing one extra deletion attempt.
3. `deepClone` uses `for...in` without an own-property guard and can clone inherited enumerable properties.

Extra edge-case finding reported by some models:

- `isEmpty(null)` and `isEmpty(undefined)` throw because `Object.keys` cannot accept them.

## Model Results

### Raw Run Summary

| MR | Model | Run ID | Status | Findings | Latency |
| --- | --- | ---: | --- | ---: | ---: |
| `!7` | `minimax-review` | `28` | `requested_changes` | `8` | `90065 ms` |
| `!7` | `claude-opus-4-6` | `29` | `completed` | `0` | `51707 ms` |
| `!7` | `openai-gpt-5-4` | `30` | `requested_changes` | `5` | `51452 ms` |
| `!8` | `minimax-review` | `31` | `requested_changes` | `2` | `31184 ms` |
| `!8` | `claude-opus-4-6` | `32` | `requested_changes` | `3` | `69818 ms` |
| `!8` | `openai-gpt-5-4` | `33` | `completed` | `0` | `8669 ms` |
| `!9` | `minimax-review` | `34` | `requested_changes` | `3` | `42491 ms` |
| `!9` | `claude-opus-4-6` | `35` | `requested_changes` | `4` | `35313 ms` |
| `!9` | `openai-gpt-5-4` | `36` | `requested_changes` | `3` | `47383 ms` |

### Behavior By Model

#### MiniMax

Strengths:

- Highest recall on `MR !7`.
- Consistently found the core off-by-one defects.
- Also reported some adjacent validation and semantics issues.

Weaknesses:

- Tends to split edge-case and semantic concerns into additional findings, so counts run higher.

Overall:

- Best recall in this sample.
- Needs human review to separate true defects from lower-priority edge cases.

#### Claude Opus

Strengths:

- After the schema-compatibility fix, provider execution became stable.
- On `MR !8` and `MR !9`, findings were reasonable and included useful edge-case analysis.

Weaknesses:

- Missed all confirmed defects on `MR !7`.
- Recall was inconsistent across MRs.

Overall:

- Transport and structured output are now working.
- Review quality is usable but not yet reliable enough to treat as the sole reviewer.

#### GPT-5.4

Strengths:

- Findings were generally concise and tightly scoped.
- On `MR !7` and `MR !9`, it captured the main production-relevant defects with low noise.

Weaknesses:

- Missed one confirmed bug in `MR !7`.
- Missed all confirmed bugs in `MR !8`.

Overall:

- Most conservative model in this sample.
- Lower noise, but recall is not sufficient by itself.

## Why The Finding Counts Differ

The count gap is caused by two different effects:

1. Different recall.
   - `claude-opus-4-6` on `MR !7` and `openai-gpt-5-4` on `MR !8` clearly under-reported confirmed defects.

2. Different granularity.
   - `minimax-review` is more willing to emit extra edge-case or semantics findings.
   - `claude-opus-4-6` sometimes emits one extra boundary-condition finding.
   - `openai-gpt-5-4` is the most conservative and usually emits fewer, narrower findings.

This means raw finding count is not a quality metric by itself. The better signal is:

- Which confirmed core defects were found
- Which edge cases were added
- Whether the model missed obvious crashes or out-of-bounds behavior

## Current Assessment

- `minimax-review` is the strongest default recall baseline in this sample.
- `claude-opus-4-6` is now technically integrated and passes end-to-end execution, but its recall still needs more observation.
- `openai-gpt-5-4` is usable as a conservative second opinion, not yet as a sole gate.

Recommended operating mode for now:

- Keep multi-model evaluation available.
- Do not interpret fewer findings as better quality.
- When comparing models, evaluate overlap on confirmed core defects before comparing raw counts.
