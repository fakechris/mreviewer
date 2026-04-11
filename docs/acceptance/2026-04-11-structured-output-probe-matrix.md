# Structured Output Probe Matrix

Date: 2026-04-11

## Scope

This note records live route-level probes run with the repo-tracked `mreviewer structured-output-probe` command. The goal was to verify that the command itself works end to end, and to capture current provider behavior before recommending any `output_mode: json_schema` default.

Probe schema:

```json
{
  "type": "object",
  "properties": {
    "verdict": { "type": "string", "enum": ["pass", "fail"] },
    "score": { "type": "number" }
  },
  "required": ["verdict", "score"],
  "additionalProperties": false
}
```

All runs used temporary local config plus environment variables only. No live credential is stored in this repository.

## Commands

```bash
mreviewer structured-output-probe --config /tmp/probe.yaml --route zhipu_probe --mode tool --runs 3
mreviewer structured-output-probe --config /tmp/probe.yaml --route zhipu_probe --mode native --runs 3
mreviewer structured-output-probe --config /tmp/probe.yaml --route minimax_probe --mode tool --runs 5
mreviewer structured-output-probe --config /tmp/probe.yaml --route minimax_openai_probe --mode tool --runs 5
mreviewer structured-output-probe --config /tmp/probe.yaml --route minimax_openai_probe --mode native --runs 5
```

## Summary

| Route | Wire API | Mode | HTTP OK | Parsed OK | Schema OK | Observed Model | Notes |
| --- | --- | --- | ---: | ---: | ---: | --- | --- |
| `zhipu_probe` | OpenAI-compatible | `tool` | `3/3` | `3/3` | `3/3` | `glm-5.1` | Synthetic `StructuredOutput` tool path worked cleanly. |
| `zhipu_probe` | OpenAI-compatible | `native` | `3/3` | `3/3` | `3/3` | `glm-5.1` | Returned fenced JSON in `message.content`; local parsing and schema validation passed. |
| `minimax_probe` | Anthropic-compatible | `tool` | `5/5` | `5/5` | `5/5` | `MiniMax-M2.7-highspeed` | Closest match to Claude Code's synthetic tool path. |
| `minimax_openai_probe` | OpenAI-compatible | `tool` | `5/5` | `5/5` | `5/5` | `MiniMax-M2.7-highspeed` | Returned valid tool calls, even when `<think>` text was present. |
| `minimax_openai_probe` | OpenAI-compatible | `native` | `5/5` | `0/5` | `0/5` | `MiniMax-M2.7-highspeed` | Always emitted `<think>`-prefixed text instead of clean JSON. |

## Authentication Sanity Check

Before the successful Zhipu acceptance run, one local replacement credential returned:

- HTTP `401`
- body: `{"error":{"code":"1000","message":"身份验证失败。"}}`

This was useful in its own right because it showed the new probe command surfaces transport and auth failures directly, instead of hiding them behind generic parsing errors.

## Representative Results

### Zhipu tool mode

Representative success:

- `finish_reason: "tool_calls"`
- tool name: `StructuredOutput`
- arguments: `{"verdict":"pass","score":0.93}`

Interpretation:

- For simple schema probes, Zhipu's OpenAI-compatible endpoint handled the Claude Code style synthetic-tool contract correctly.

### Zhipu native mode

Representative success:

```text
message.content:
{
  "verdict": "pass",
  "score": 0.93
}
```

Interpretation:

- The provider-native path produced schema-valid JSON text for this minimal schema.
- The payload still arrived as assistant text, not a tool call, so we still rely on local parsing and validation.
- This is enough to say "simple JSON-schema compatibility works in this probe", but not enough to promote `json_schema` to the global default for more complex review payloads.

### MiniMax Anthropic tool mode

Representative success:

- `stop_reason: "tool_use"`
- tool name: `StructuredOutput`
- input: `{"verdict":"pass","score":0.93}`

Interpretation:

- This is the cleanest fit for the Claude Code style structured-output contract.
- It is currently the most production-ready MiniMax route in this matrix.

### MiniMax OpenAI native mode

Representative failure:

- `finish_reason: "stop"`
- content began with `<think>`
- local parse error: `invalid character '<' looking for beginning of value`

Interpretation:

- HTTP success is not enough.
- On this route, `response_format.type=json_schema` still did not produce directly parseable JSON.

## Decision

Current production guidance remains:

- Default OpenAI-compatible routes to `tool_call`.
- Treat `json_schema` as an opt-in strategy that must be locally probed first.
- Keep MiniMax Anthropic-compatible routes on the synthetic tool path.
- Allow Zhipu `json_schema` only after route-level probe evidence for the exact endpoint and schema size you plan to ship.

This complements, rather than replaces, the earlier full-run Zhipu note:

- [docs/acceptance/2026-04-10-zhipu-glm51-structured-output-probe.md](./2026-04-10-zhipu-glm51-structured-output-probe.md)

That earlier note used a stricter comparison harness and showed why minimal-schema success should not be over-generalized to full review payloads.
