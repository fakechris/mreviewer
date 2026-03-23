# MiniMax JSON Compliance Cases

> English version of the current MiniMax structured-output regression notes.
>
> Chinese source document:
> [docs/minimax-json-compliance-cases.md](./minimax-json-compliance-cases.md)

This document only records the current model-capability boundary we still observe with MiniMax under the new review pipeline:

> forced tool call + strict schema validation + one repair retry

This document does not discuss:

- parser, writer, scheduler, database, or GitLab API engineering issues
- the legacy prompt-only JSON approach
- temperature theory in general

The legacy prompt-only cases have been moved to:
- [docs/minimax-legacy-prompt-json-cases.md](./minimax-legacy-prompt-json-cases.md)

## Current Structured-Output Path

The current review pipeline is no longer asking the assistant to emit free-text JSON.

It now works like this:

1. Force a single tool call: `submit_review`
2. Attach a strict JSON schema to the tool input
3. Validate the returned tool input on the server side
4. If the first response is invalid, issue exactly one repair retry
5. If the repaired output is still invalid, fail the run

That means:

- if we still see schema drift, it is no longer just a weak-prompt problem
- any `repair_retry` means the model did not satisfy the strict schema on the first pass

## Exact Request Shape We Send

The actual MiniMax request payload in the current implementation is:

```json
{
  "model": "MiniMax-M2.7-highspeed",
  "max_tokens": 4096,
  "temperature": 0.20,
  "system": "<dynamic review system prompt>",
  "messages": [
    {
      "role": "user",
      "content": "<JSON-encoded review request>"
    }
  ],
  "tools": [
    {
      "name": "submit_review",
      "description": "Emit the final review result",
      "input_schema": "<strict review result schema>"
    }
  ],
  "tool_choice": {
    "type": "tool",
    "name": "submit_review"
  }
}
```

The important part is not just that tools are enabled. We also force `tool_choice` to the single output tool, so the model is not expected to answer in free text.

## Tool Input We Expect Back

The tool input must satisfy this top-level structure:

```json
{
  "schema_version": "1.0",
  "review_run_id": "27",
  "summary": "High-level review summary",
  "findings": [
    {
      "category": "bug",
      "severity": "high",
      "confidence": 0.95,
      "title": "Issue title",
      "body_markdown": "Why this is a real issue and how to fix it",
      "path": "src/file.ts",
      "anchor_kind": "added_line",
      "new_line": 35,
      "anchor_snippet": "for (let i = 0; i <= rules.length; i++) {",
      "evidence": [
        "supporting code snippet 1",
        "supporting code snippet 2"
      ],
      "suggested_patch": "for (let i = 0; i < rules.length; i++) {",
      "trigger_condition": "What exact code pattern triggered this finding",
      "impact": "What happens if this is not fixed",
      "introduced_by_this_change": true
    }
  ]
}
```

Required top-level fields:

- `schema_version`
- `review_run_id`
- `summary`
- `findings`

Required fields for each finding:

- `category`
- `severity`
- `confidence`
- `title`
- `body_markdown`
- `path`
- `anchor_kind`

The tool input is also validated with `additionalProperties=false`, so extra fields are not accepted silently.

## Tool Call Output We Actually Parse

At the transport level, the response we expect from the Anthropic-compatible API is a `tool_use` block named `submit_review`. In simplified form:

```json
{
  "id": "msg_1",
  "content": [
    {
      "type": "tool_use",
      "id": "toolu_1",
      "name": "submit_review",
      "input": {
        "schema_version": "1.0",
        "review_run_id": "27",
        "summary": "Review summary",
        "findings": []
      }
    }
  ],
  "usage": {
    "output_tokens": 42
  }
}
```

If the model does not return a `tool_use` block with the expected name, we treat that as a structured-output failure, not as a normal successful review response.

## Real Regression Scope

- model: `MiniMax-M2.7-highspeed`
- temperature: `0.20`
- output channel: `submit_review` tool call
- language: `zh-CN`
- GitLab MR samples:
  - `songchuansheng/case-revenue-bridge!3`
  - `songchuansheng/case-revenue-bridge!4`
  - `songchuansheng/case-revenue-bridge!5`
  - `songchuansheng/case-revenue-bridge!6`

## Result Summary

| MR | Run | Final status | Provider latency | Tokens | fallback_stage | Result |
| --- | --- | --- | ---: | ---: | --- | --- |
| `!3` | `24` | `requested_changes` | `39529 ms` | `2590` | `repair_retry` | Succeeded, but first-pass structure was invalid |
| `!4` | `25` | `requested_changes` | `56033 ms` | `3853` | `repair_retry` | Succeeded, but first-pass structure was invalid |
| `!5` | `26` | `requested_changes` | `25801 ms` | `1665` | `repair_retry` | Succeeded, but first-pass structure was invalid |
| `!6` | `27` | `failed` | `57630 ms` | `4175` | `repair_retry` then failed | Model still did not satisfy schema after repair |

Statistics:

- total samples: `4`
- final success: `3/4`
- final failure: `1/4`
- successful samples that still required `repair_retry`: `3/3`
- samples that satisfied the strict schema on the first pass: `0/4`

## What We Still Observe

### 1. First-pass tool-call output is still unstable

MR `!3`, `!4`, and `!5` all completed successfully, but none of them passed on the first attempt. All three entered `repair_retry`.

That means MiniMax still produces:

- first-pass tool arguments that fail strict schema validation
- outputs that only become usable after a second repair round

So the new pipeline has improved system-level reliability, but it has not made the model itself first-pass schema-stable.

### 2. Even after repair, required fields can still be missing

MR `!6` is the most important failure case in this round.

- run: `27`
- final status: `failed`
- error code: `provider_request_failed`

Raw validation error:

```text
llm: strict validation failed after repair:
$.findings[0].body_markdown is required;
$.findings[1].body_markdown is required;
$.findings[2].body_markdown is required;
$.findings[3].body_markdown is required;
$.findings[4].body_markdown is required;
$.findings[5].body_markdown is required;
$.findings[6].body_markdown is required
```

This is not a case where the model returned no structure at all. It returned seven findings, but after repair every finding was still missing the required `body_markdown` field.

Problematic raw finding sample:

```json
{
  "anchor_kind": "added_line",
  "anchor_snippet": "for (let i = 0; i <= rules.length; i++) {",
  "category": "logic_error",
  "confidence": 0.95,
  "evidence": [
    "for (let i = 0; i <= rules.length; i++) {",
    "const rule = rules[i]; // when i === rules.length, rule is undefined",
    "const value = record[rule.field]; // accessing a property of undefined throws"
  ],
  "impact": "If rules has length n, the loop runs n+1 times. When i === n, rules[n] is undefined and accessing rule.field throws a TypeError.",
  "introduced_by_this_change": true,
  "new_line": 35,
  "path": "src/lib/dataValidator.ts",
  "severity": "critical",
  "suggested_patch": "for (let i = 0; i < rules.length; i++) {",
  "title": "Loop boundary bug: using <= causes an out-of-bounds access",
  "trigger_condition": "Whenever rules.length > 0, the last iteration dereferences undefined"
}
```

This object already contains many of the fields we want, but it still violates the strict schema because `body_markdown` is missing.

That shows:

- MiniMax is not completely unable to produce structured content
- but it still violates required-field constraints at the field level
- even one repair retry does not guarantee that all required fields will be present

### 3. The model can identify real issues, but cannot package them reliably into the strict schema

Across `!3`, `!4`, `!5`, and `!6`, the model is not completely blind to the code issues.

It can often identify real problems such as:

- out-of-bounds access
- wrong field reference
- missing type branch handling
- prototype pollution risk

The unstable part is the structured-output packaging:

- first-pass schema compliance
- field completeness after repair

So the current bottleneck is better described as:

> not “the model cannot reason about the bug”, but “the model cannot reliably serialize its reasoning into the strict review schema”.

## What We Can Conclude Now

### What the new pipeline has already improved

Compared with the legacy prompt-only JSON approach, we have clearly improved:

- we no longer depend on free-text JSON responses
- we no longer rely on prompt wording alone to coerce output structure
- real GitLab MRs can run end to end
- successful runs can now land as `requested_changes` with persisted findings

### What is still not solved

These issues are still real:

- MiniMax does not reliably satisfy the strict schema on the first pass
- `repair_retry` is not rare; it is the norm in the successful samples from this round
- even after repair, required fields can still be missing, which can still fail the run

So the most accurate current conclusion is:

> The new pipeline has substantially improved system-level reliability, but MiniMax itself still does not meet the bar of stable strict-schema output.

## The Most Useful Questions for the MiniMax Team

1. In Anthropic-compatible tool-call mode, is the schema treated only as model guidance, or is any part of it enforced server-side?
2. Why would the model still systematically omit a required field such as `body_markdown` even after a repair round that explicitly includes the validation error?
3. Are there known schema-compliance failure modes in MiniMax tool arguments, especially when the `findings` array is longer?
4. Are there official best practices for `MiniMax-M2.7-highspeed` structured output that materially reduce the `repair_retry` rate?

## Community-Friendly Question Framing

This is the short version we can ask in public:

> We moved our MiniMax review pipeline from free-text JSON to a single forced tool call + strict schema validation + one repair retry. On 4 real GitLab MR regressions with `MiniMax-M2.7-highspeed` at `temperature=0.20`, 3 runs finally succeeded but all required `repair_retry`, and 1 still failed after repair because every finding was missing `body_markdown`. Has anyone seen similar behavior? What improved tool-argument schema compliance for you?

## One-Line Conclusion

> The new pipeline proves that the system can absorb part of MiniMax’s schema drift, but based on these 4 real MRs, MiniMax is still not a model that consistently produces strict-schema output on its own.
