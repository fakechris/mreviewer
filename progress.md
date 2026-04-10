# Progress

## 2026-04-09

- Confirmed live schema benchmark command exists and uses real provider calls.
- Confirmed package-wide Docker-backed suites are not usable for this environment.
- Next: create benchmark input JSONL and isolated Zhipu config, then run live requests.
- Ran live direct probes for `glm-5` and `glm-5.1` against Zhipu coding endpoint.
- Observed intermittent `429` congestion, but also successful `200` responses for `tool_call + auto`.
- Observed `200` responses for `json_schema strict=true` that still returned non-JSON plain text, indicating unreliable strict-schema enforcement even when congestion is not the blocking factor.

## 2026-04-10

- Ran a 10-sample `glm-5.1` benchmark for `tool_call_auto` and `json_schema_strict`.
- `tool_call_auto` summary: `5/10` HTTP `200`, `5/10` schema-valid, `5/10` rate-limited.
- `json_schema_strict` summary: `1/10` HTTP `200`, `0/10` schema-valid, `9/10` rate-limited.
- Wrote the benchmark conclusion into `docs/acceptance/2026-04-10-zhipu-glm51-structured-output-probe.md`.
- Updated product-facing docs to recommend `tool_call` for `zhipuai` and to avoid positioning strict schema as the default path.
