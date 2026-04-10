# Findings

## 2026-04-09

- `cmd/mreviewer/schema_benchmark.go` already provides a live route benchmark for serialized `ReviewRequest` JSONL inputs.
- Zhipu official docs state `glm-5` supports `Function Call` and `结构化输出`.
- Zhipu's OpenAI-compatible tool-calling docs indicate `tool_choice` is effectively limited to `auto`, which matches the compat mode already added in this branch.
- Live probe against `https://open.bigmodel.cn/api/coding/paas/v4/chat/completions` showed `tool_call + auto` can succeed for both `glm-5` and `glm-5.1` after intermittent `429/code=1305` congestion responses.
- Live probe against `json_schema strict=true` produced at least one `200` response for both `glm-5` and `glm-5.1`, but the body still ignored the requested schema and returned plain text / fenced text rather than strict JSON.
- Current evidence therefore supports: transport is fine, congestion is real, but strict JSON schema adherence is not reliable on this endpoint/model path.

## 2026-04-10

- A 10-sample live benchmark for `glm-5.1` was captured in `/tmp/glm51_benchmark_2026-04-10.json`.
- `tool_call_auto`: `5/10` samples reached `200`, and all `5/5` of those `200` responses produced valid function-call arguments. The other `5/10` samples were rate-limited with `429/code=1305`.
- `json_schema_strict`: `1/10` samples reached `200`, `0/10` produced schema-valid JSON, and `9/10` samples were rate-limited with `429/code=1305`.
- The one `json_schema_strict` `200` response still ignored the requested schema and returned plain text: `verdict=pass and score=0.93`.
- Current recommendation: keep `zhipuai` configured around `tool_call`, and treat `json_schema strict=true` as non-production / experimental on this endpoint until Zhipu behavior changes.
