# Zhipu GLM-5.1 Structured Output Probe

Date: 2026-04-10

## Scope

This note records a live probe against Zhipu's official coding endpoint:

- endpoint: `https://open.bigmodel.cn/api/coding/paas/v4/chat/completions`
- model: `glm-5.1`
- modes compared:
  - `tool_call_auto`
  - `json_schema_strict`
- samples per mode: `10`

Raw benchmark artifact:

- local-only capture during the 2026-04-10 run: `/tmp/glm51_benchmark_2026-04-10.json`
- repo-tracked canonical summary: this document

## Summary

| Mode | HTTP 200 | Schema-valid | Invalid 200 | Rate-limited |
| --- | ---: | ---: | ---: | ---: |
| `tool_call_auto` | 5/10 | 5/10 | 0/10 | 5/10 |
| `json_schema_strict` | 1/10 | 0/10 | 1/10 | 9/10 |

Derived rates:

- `tool_call_auto`
  - `http_200_rate = 0.5`
  - `schema_valid_rate = 0.5`
  - `schema_valid_given_200_rate = 1.0`
- `json_schema_strict`
  - `http_200_rate = 0.1`
  - `schema_valid_rate = 0.0`
  - `schema_valid_given_200_rate = 0.0`

## Representative Results

### `tool_call_auto`

Successful samples: `1, 3, 4, 6, 9`

Representative success pattern:

- `finish_reason: "tool_calls"`
- returned function name: `submit_result`
- returned arguments: `{"verdict":"pass","score":0.93}`

Interpretation:

- Zhipu congestion is real on this endpoint, but when a `200` response lands, the `tool_call` path can produce valid structured arguments.

### `json_schema_strict`

Only one sample reached `200`, and it still failed schema compliance.

Representative failure pattern:

- `finish_reason: "stop"`
- assistant content: `verdict=pass and score=0.93`
- failure note: `non_json_content:verdict=pass and score=0.93`

Interpretation:

- This is not just a transport or congestion problem.
- Even without a `429`, the model/endpoint path can ignore `response_format.json_schema.strict=true` and emit plain text instead of schema-valid JSON.

## Decision

For `zhipuai` in `mreviewer`:

- prefer `tool_call`
- keep `tool_choice: "auto"`
- do not present `json_schema strict=true` as the default or recommended production path

## Practical Implication

The current branch should ship Zhipu support as an OpenAI-compatible `tool_call` integration, with docs that explicitly call out the current strict-schema limitation. If Zhipu later improves strict JSON-schema adherence on this endpoint, we can re-run the same probe and revisit the recommendation.
