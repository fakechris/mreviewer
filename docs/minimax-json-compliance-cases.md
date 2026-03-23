# MiniMax JSON Compliance Cases

本文档只记录当前新方案下，MiniMax 在“强制 tool call + 严格 schema 校验 + repair retry”这条链路里，仍然表现出的模型能力边界。

这里不讨论：

- parser、writer、状态机、数据库、GitLab API 等工程问题
- 旧的 prompt-only JSON 输出方案
- temperature 的理论讨论

旧方案的历史案例已经迁到：
- [docs/minimax-legacy-prompt-json-cases.md](./minimax-legacy-prompt-json-cases.md)

## 当前方案

当前 review 生成链路已经不是“让 assistant 文本直接吐 JSON”，而是：

1. 强制调用单一工具 `submit_review`
2. 工具参数带严格 schema
3. 服务端对返回结果做严格校验
4. 首次不合法时执行一次 repair retry
5. repair 后仍不合法则整次 run 失败

这意味着：

- 如果仍然出现结构漂移，那已经不是“prompt 写得不够硬”这么简单
- 只要出现 `repair_retry`，就代表模型首轮输出没有直接满足严格 schema

## 本轮真实回归范围

- 模型：`MiniMax-M2.7-highspeed`
- temperature：`0.20`
- 输出通道：`submit_review` tool call
- 语言：`zh-CN`
- 样本 MR：
  - `songchuansheng/case-revenue-bridge!3`
  - `songchuansheng/case-revenue-bridge!4`
  - `songchuansheng/case-revenue-bridge!5`
  - `songchuansheng/case-revenue-bridge!6`

## 结果总览

| MR | Run | 终态 | provider latency | tokens | fallback_stage | 结果判断 |
| --- | --- | --- | ---: | ---: | --- | --- |
| `!3` | `24` | `requested_changes` | `39529 ms` | `2590` | `repair_retry` | 成功，但首轮结构不合规 |
| `!4` | `25` | `requested_changes` | `56033 ms` | `3853` | `repair_retry` | 成功，但首轮结构不合规 |
| `!5` | `26` | `requested_changes` | `25801 ms` | `1665` | `repair_retry` | 成功，但首轮结构不合规 |
| `!6` | `27` | `failed` | `57630 ms` | `4175` | `repair_retry` 后仍失败 | 模型最终仍未满足 schema |

统计：

- 总样本：`4`
- 最终成功：`3/4`
- 最终失败：`1/4`
- 成功样本里需要 `repair_retry`：`3/3`
- “一次就严格满足 schema”的样本：`0/4`

## 观察到的当前问题

### 1. 首轮 tool-call 输出仍然不稳定

MR `!3`、`!4`、`!5` 都最终完成了，但三条都不是首轮直接通过，而是进入了 `repair_retry`。

这说明在新方案下，MiniMax 仍然会出现：

- 首轮工具参数不满足严格 schema
- 需要第二轮修复才能落地

也就是说，新方案解决的是“系统级可用性”，不是“模型首轮就稳定遵循 schema”。

### 2. repair 之后仍可能漏必填字段

MR `!6` 是这轮最关键的失败样本。

- Run：`27`
- 最终状态：`failed`
- error_code：`provider_request_failed`

原始错误：

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

也就是说，这次不是“完全没有结构”，而是模型输出了 7 条 findings，但在 repair 之后仍然漏掉了所有 finding 的必填字段 `body_markdown`。

响应片段：

```json
{
  "anchor_kind": "added_line",
  "anchor_snippet": "for (let i = 0; i <= rules.length; i++) {",
  "category": "logic_error",
  "confidence": 0.95,
  "evidence": [
    "for (let i = 0; i <= rules.length; i++) {",
    "const rule = rules[i]; // 当 i === rules.length 时，rule 为 undefined",
    "const value = record[rule.field]; // 访问 undefined 的属性会抛出错误"
  ],
  "impact": "当 rules 数组长度为 n 时，循环会执行 n+1 次。当 i === n 时，rules[n] 为 undefined，访问 rule.field 会导致 TypeError。",
  "introduced_by_this_change": true,
  "new_line": 35,
  "path": "src/lib/dataValidator.ts",
  "severity": "critical",
  "suggested_patch": "for (let i = 0; i < rules.length; i++) {",
  "title": "循环边界错误：使用 <= 导致数组越界访问",
  "trigger_condition": "当 rules 数组长度大于 0 时，循环最后一次迭代访问 undefined 的属性"
}
```

这个对象已经包含了很多我们想要的字段，但仍然缺少必填的 `body_markdown`。这说明：

- 当前 MiniMax 并不是“完全不会输出结构化内容”
- 但它仍会在局部字段上违背严格 schema
- 即使经过 repair，也不保证能补齐所有必填字段

### 3. 模型能给出正确问题，但不能稳定按 schema 包装

从 `!3`、`!4`、`!5`、`!6` 这四条看，模型对“代码里确实有哪些 bug”并不是完全失明。

它经常能识别：

- 数组越界
- 错误字段引用
- 类型分支遗漏
- 原型污染风险

但真正不稳定的是：

- 首轮 structured output 包装
- repair 之后的字段完整性

因此，当前瓶颈更像是：

> 不是“问题识别能力完全不行”，而是“识别结果到严格 schema 的落盘稳定性不够”。

## 当前可下的结论

### 已经解决到的程度

相对旧方案，现在已经明确改善了：

- 不再依赖自由文本 JSON
- 不再靠 prompt 单独硬逼 assistant 文本
- 真实 MR 可以多次端到端跑通
- 成功样本能够稳定落成 `requested_changes`

### 还没有解决的程度

但以下问题仍然真实存在：

- MiniMax 不能稳定首轮命中严格 schema
- repair retry 不是偶发，而是这轮成功样本里的常态
- repair 之后仍可能漏掉必填字段，导致整次 run 失败

因此，当前最准确的判断是：

> 新方案已经把系统层可用性显著提高了，但 MiniMax 本身仍然没有达到“严格 schema 稳定输出”的水平。

## 对 MiniMax 官方最值得问的问题

1. 在 Anthropic-compatible 的 tool-call 模式下，`strict` schema 是否只是提示，而不是服务端强约束？
2. 为什么在同一条 review schema 下，模型会在 repair 后仍然系统性漏掉 `body_markdown` 这类必填字段？
3. MiniMax 对 tool arguments 的 schema 遵循，是否存在已知的不稳定场景，尤其是 findings 数组较长时？
4. `MiniMax-M2.7-highspeed` 是否有官方推荐的 structured-output 最佳实践，能显著降低 repair retry 比例？

## 对社区可直接使用的问题表述

可以直接这样提问：

> 我们已经把 MiniMax review 输出从“自由文本 JSON”改成了“单一 tool call + 严格 schema 校验 + repair retry”。在 `MiniMax-M2.7-highspeed`、`temperature=0.20` 下，对 4 条真实 GitLab MR 回归，3 条最终成功但全部依赖 repair retry，1 条在 repair 后仍然因为缺少 `body_markdown` 失败。有没有人遇到过类似问题？你们是怎么把 tool-call 参数稳定性再往上提的？

## 一句话结论

> 新方案已经证明系统可以兜住 MiniMax 的一部分结构漂移，但从这 4 条真实 MR 看，MiniMax 仍然不是“严格 schema 稳定输出”的模型。
