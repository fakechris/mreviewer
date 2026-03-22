# MiniMax Known Issues

本文档记录当前 `mreviewer` 接 MiniMax Anthropic-compatible 接口时，已经确认的行为、稳定性判断和后续待办。

## 当前结论

截至目前，MiniMax 集成已经可以跑通真实 GitLab MR review，并完成：

- 拉取 MR diff / 上下文
- 调用 MiniMax Anthropic-compatible 接口
- 落库 `review_runs` / `review_findings`
- 回写 GitLab inline discussion / summary note
- 在 `audit_logs.detail` 中保留完整 provider request / response

但它还不能算“完全稳定”。

更准确的判断是：

- 对输出严格遵循既定 JSON schema 的请求，链路已经稳定
- 对输出 schema 会漂移的请求，系统已经比之前稳很多，但仍需要继续补兼容层
- 当前适合继续灰度验证，不适合宣称“MiniMax 集成已经完全稳定”

## 已确认问题

### 1. MiniMax 输出 schema 会漂移

真实运行中已经观察到同一条 MR 在不同调用里出现不同字段形状，包括：

- `type` 替代 `category`
- `file_path` 替代 `path`
- `line_start` / `line_end` 替代 `new_line` / `old_line`
- `line_number` 替代 `new_line` / `line_start`
- `actionable_fix` 替代 `suggested_patch`
- `evidence` 返回字符串而不是字符串数组
- `fingerprint` 替代现有 finding 指纹字段
- 完全省略 `category/type`

这意味着不能只依赖 prompt 约束，必须在 parser 层做兼容归一化。

### 2. parser-error 路径以前会丢原始现场

早期实现中，provider 解析失败时：

- `review_runs.error_detail` 只能看到简短错误
- 无法直接拿到完整 provider response
- 无法复盘 provider request 全量内容

当前这条链路已经补齐：

- `provider_called` 审计保存完整 request / response
- `provider_failed` 审计保存完整 request
- 如果是解析失败，`provider_failed` 还会保存完整原始 response 文本

### 3. parser-error 路径以前会污染指标

早期 parser-error fallback 使用了伪造的 latency / tokens，而不是 provider 的真实值。  
这会让排障和成本分析失真。

当前实现已经开始把真实 provider 现场沿 parser-error 路径传递，但这块仍需要继续观察真实运行结果。

补充说明：

- 对 parser 失败的 run，真实 `provider_latency_ms` / `provider_tokens_total` 已经会进入 `audit_logs`
- 但 `review_runs` 主记录在 terminal provider failure 路径下仍可能是 `0`
- 这意味着现场已经能完整复盘，但主记录指标还没有完全统一

### 4. MiniMax `thinking` 能力文档存在冲突

官方 Anthropic-compatible 文档一方面展示了 `thinking` block / `thinking_delta`，另一方面又在兼容性说明里对 `thinking` 参数是否真正生效存在冲突表述。

因此目前不应把 `thinking` 当成一个已经确认可靠的生产开关。

## 当前稳定性判断

目前状态：

- GitLab E2E review 主链路：可用
- MiniMax 原始现场审计：可用
- 针对已知字段别名的 parser 兼容：已补
- 对“未在样本中见过的新 schema 漂移”容错：仍需继续加固
- 对有 bug 的真实 MR（如 `case-revenue-bridge!2`）重复运行后，仍可能因为新一轮 schema 漂移而失败

一句话总结：

> 现在已经从“经常现场盲飞”提升到“出了问题能完整复盘，并且能处理一部分真实 schema 漂移”；但还没到“随便什么 MiniMax 输出都能稳定吃下”的程度，也还不能宣称对真实有 bug 的 MR 已经稳定。

## TODO

- [ ] 继续收集真实 MiniMax 输出样本，把更多字段漂移固化为回归测试
- [ ] 为 parser 增加更系统的 schema-normalization 层，而不是按单字段打补丁
- [ ] 对 parser-error 失败路径补充真实 latency / token 持久化回归
- [ ] 评估是否要引入“二次 JSON 修复”步骤，用于处理轻微格式漂移
- [ ] 评估 MiniMax `thinking` 是否值得作为实验开关接入，而不是默认开启
- [ ] 为不同模型版本（如 `MiniMax-M2.5` / `MiniMax-M2.7`）分别记录稳定性差异
