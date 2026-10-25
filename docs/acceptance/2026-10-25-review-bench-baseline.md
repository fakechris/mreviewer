# Review bench 基线（INV-1020）

日期：2026-10-25。命令：`mreviewer bench review`（`cmd/mreviewer/bench.go`，`internal/reviewbench`）。

## 1. 评测集

`testdata/review-bench/`，三类 case，每个 case 一个目录一个 `case.json`，`added_at` 字段支持 `--since` 增量评测。

| 目录 | 来源 | 数量 | 用途 |
|---|---|---|---|
| `aacr/` | [AACR-Bench](https://huggingface.co/datasets/Alibaba-Aone/aacr-bench)（Apache 2.0，arXiv 2601.19494）抽样，`scripts/review-bench/build_aacr_cases.py` 用 `gh api` 取 `pr_source_commit...pr_target_commit` 的 compare diff 落盘 | 40 个 PR，10 种语言（每种 ≤6 个），改动 ≤600 行，共 461 条专家确认的 golden、113 条已知错误评论（negatives） | 主评测：precision / recall / F1 |
| `zh/` | 自建中文样本，`scripts/review-bench/build_zh_cases.py`，中文注释与拼音标识符，每个 1-2 处植入缺陷，golden 用中文工程师口吻写 | 10 个（Go 3、Python 3、TypeScript 2、Java 2） | 中文自然度与术语保留；也计入 totals |
| `control/` | 本仓库历史上的纯重构 / 纯文档提交，`scripts/review-bench/build_control_cases.py` | 14 个 | 对照组：没有 golden，所有 finding 都算误报 |

AACR-Bench 的 `pr_source_commit` 是 base、`pr_target_commit` 是被评审的修订，`side=right` 的行号指向后者的新文件；已用 compare API 的 ahead/behind 核实方向。评测集不需要网络。

## 2. 匹配规则（AACR-Bench 未公开评测脚本，此处为本仓库口径）

对每条 finding 与每条 golden：

1. 路径相同（去掉前导 `./`），diff side 兼容（finding 的 new/空 ↔ 评论的 right，old ↔ left）。
2. 行号窗口：finding 的 `[start, end]` 与评论的 `[from, to]` 各向外扩 ±5 行后有交集。
3. 候选对按 `|start - from|` 升序贪心配对，每条 finding 与每条 golden 最多用一次。
4. 若配置了 `--judge-route`，每个候选对再交给 LLM judge 判定“是否同一问题”（temperature 0，JSON 输出）；judge 报错按不匹配计并计数，不会算成命中。
5. 未命中 golden 的 finding 再对 negatives 走同样流程，命中者记为 `negative_hit`（撞上已知错误评论），仍算误报。

指标：precision = TP / findings，recall = TP / golden，F1 为调和平均；每 case 平均 finding 数作为人工复核时间的代理；token 为各 pack 的 provider 用量之和。对照组单独报告误报数。中文自然度由 judge 对每 case 前 N 条 finding 打 1-5 分并判断代码术语是否被翻译。

judge 与被评路由跨厂商（DeepSeek 路由用 MiniMax 做 judge，反之亦然），避免同一模型自评。

## 3. 路线图对照

OpenCodeReview README 榜单（同一 AACR-Bench 全集 200 PR，其自有匹配口径，厂商自测）：

| 宿主 | 模型 | F1 | Precision | Recall | 平均 token |
|---|---|---|---|---|---|
| OCR v1.3.1 | DeepSeek-V4-Pro | 17.9% | 30.6% | 12.7% | 394K |
| OCR v1.3.1 | GLM-5.2 | 21.3% | 32.3% | 15.9% | 682K |
| Claude Code v2.1.169 | DeepSeek-V4-Pro | 10.9% | 8.3% | 16.1% | 5,450K |

注意两点差异：本仓库只抽样了 40 个 PR 且限制 ≤600 行，匹配口径不同，数字只能做量级参考，不能直接排名。

## 4. 基线结果

（由 `bench review --output markdown` 生成。）

### 4.1 DeepSeek V4 Pro（`deepseek_v4_pro`，provider `deepseek`，thinking 关闭 + 强制 submit_review；judge MiniMax-M2.7-highspeed）

完整报告：`docs/acceptance/review-bench/2026-10-25-deepseek_v4_pro.json`（含每条 finding 原文，可用 `--findings` 重新评分）。

| 指标 | 值 |
|---|---|
| 评测 case（golden + chinese） | 50 |
| golden 总数 | 476 |
| 报告 finding 数 | 131 |
| 命中（TP） | 37 |
| Precision | 28.2% |
| Recall | 7.8% |
| F1 | 12.2% |
| 每 case 平均 finding 数 | 2.6 |
| 每 case 平均 token | 3340 |
| 撞上已知错误评论 | 1 |
| 对照组（14 个纯重构/文档提交）误报 | 9（0.6/case） |
| 中文自然度（1-5） | 4.93，122 条 finding；术语被翻译 1 条 |
| 中文样本（10 个）命中 | 11/15 golden，42 条 finding |
| 引擎错误 / judge 错误 | 0 / 5（judge 调用 89 次） |
| 越界被降级的 finding（INV-1018） | 10 |

按语言：

| 语言 | case | golden | finding | TP | Precision | Recall | F1 |
|---|---|---|---|---|---|---|---|
| C | 4 | 42 | 13 | 2 | 15.4% | 4.8% | 7.3% |
| C# | 4 | 24 | 2 | 2 | 100.0% | 8.3% | 15.4% |
| C++ | 4 | 66 | 13 | 2 | 15.4% | 3.0% | 5.1% |
| Go | 7 | 58 | 19 | 7 | 36.8% | 12.1% | 18.2% |
| Java | 6 | 53 | 20 | 5 | 25.0% | 9.4% | 13.7% |
| JavaScript | 4 | 57 | 6 | 0 | 0.0% | 0.0% | 0.0% |
| PHP | 4 | 29 | 5 | 2 | 40.0% | 6.9% | 11.8% |
| Python | 7 | 36 | 22 | 7 | 31.8% | 19.4% | 24.1% |
| Rust | 4 | 39 | 8 | 5 | 62.5% | 12.8% | 21.3% |
| TypeScript | 6 | 72 | 23 | 5 | 21.7% | 6.9% | 10.5% |

**稳定性**：同一路由的前一次完整运行（judge 为 MiniMax-M3.1-Flash，该 judge 有 59/107 次调用失败，失败按未命中计）得到 precision 26.1% / recall 6.5% / F1 10.4%；两次 F1 相差 1.8 个百分点，在验收线 5 个百分点内。注意这两次 judge 不同，是偏保守的稳定性证据。

**与 OCR 榜单同模型对照**：OCR + DeepSeek-V4-Pro 在 200 PR 全集上 F1 17.9%（P 30.6% / R 12.7%，394K token/PR）。mreviewer 在 40 PR 抽样上 F1 12.2%（P 28.2% / R 7.8%，3.3K token/case）。precision 同一量级，recall 约为其六成，token 约为其百分之一（OCR 的 agent 会读全文件并搜索调用关系，mreviewer 只看 diff 加 20 行上下文）。口径不同，仅作量级参考。

**过程中修掉的三个路由问题**（都已随本事项进入代码）：

1. DeepSeek V4 thinking 模式下 `tool_choice` 只能 `auto`，大 diff 时模型以散文作答不调工具：首轮 64 case 错 40 个（见 `thinking-auto` 运行，成功的 24 个 case F1 23.5%）。`DeepSeekCompatMode` 现在发送 `thinking: disabled` 并强制工具调用；`provider: deepseek` 此前从未被 factory 接线。
2. 关掉 thinking 后 DeepSeek 偶尔把结果包在 `{"review": ...}` 外壳里，schema harness 新增单键外壳解壳。
3. judge 用思考型模型会把预算花在推理上返回空 content：judge 对 DeepSeek 路由关闭 thinking，预算提到 400 token。

### 4.2 MiniMax M2.7-highspeed（`minimax_m27_highspeed`，provider `minimax`，Anthropic 兼容端点强制 submit_review；judge DeepSeek-Flash，thinking 关闭）

完整报告：`docs/acceptance/review-bench/2026-10-25-minimax_m27_highspeed.json`。

| 指标 | 值 |
|---|---|
| 评测 case（golden + chinese，不含出错的） | 43 |
| golden 总数 | 403 |
| 报告 finding 数 | 131 |
| 命中（TP） | 40 |
| Precision | 30.5% |
| Recall | 9.9% |
| F1 | 15.0% |
| 每 case 平均 finding 数 | 3.0 |
| 每 case 平均 token | 39576 |
| 撞上已知错误评论 | 2 |
| 对照组误报 | 17（1.2/case） |
| 中文自然度（1-5） | 4.97，123 条；术语被翻译 1 条 |
| 中文样本命中 | 14/15 golden，47 条 finding |
| 引擎错误 / judge 错误 | 7 / 0（judge 调用 105 次） |
| 越界被降级的 finding | 12 |

按语言：

| 语言 | case | golden | finding | TP | Precision | Recall | F1 |
|---|---|---|---|---|---|---|---|
| C | 1 | 11 | 1 | 0 | 0.0% | 0.0% | 0.0% |
| C# | 3 | 18 | 1 | 0 | 0.0% | 0.0% | 0.0% |
| C++ | 4 | 66 | 8 | 2 | 25.0% | 3.0% | 5.4% |
| Go | 7 | 58 | 26 | 9 | 34.6% | 15.5% | 21.4% |
| Java | 6 | 53 | 20 | 8 | 40.0% | 15.1% | 21.9% |
| JavaScript | 2 | 27 | 7 | 3 | 42.9% | 11.1% | 17.6% |
| PHP | 4 | 29 | 8 | 2 | 25.0% | 6.9% | 10.8% |
| Python | 7 | 36 | 28 | 9 | 32.1% | 25.0% | 28.1% |
| Rust | 3 | 33 | 6 | 1 | 16.7% | 3.0% | 5.1% |
| TypeScript | 6 | 72 | 26 | 6 | 23.1% | 8.3% | 12.2% |

出错的 7 个 case：aacr/dotnet__aspnetcore__61006, aacr/facebook__react__32499, aacr/linera-io__linera-protocol__4639, aacr/mpv-player__mpv__15828, aacr/mpv-player__mpv__16090, aacr/sveltejs__svelte__15250, aacr/timescale__timescaledb__8138。其中 6 个是 `missing tool_use block`：M2.7 的思考块也计入 max_tokens，20000 的预算在大 diff 上仍被耗尽（非流式 Anthropic SDK 调用把 max_tokens 卡在约 21k 以下）；1 个是修复后仍不过严格校验。这些 case 未计入指标。

### 4.3 两条路由对照

| | DeepSeek V4 Pro | MiniMax M2.7-highspeed |
|---|---|---|
| F1 | 12.2% | 15.0% |
| Precision | 28.2% | 30.5% |
| Recall | 7.8% | 9.9% |
| finding/case | 2.6 | 3.0 |
| token/case | 3340 | 39576 |
| 对照组误报/case | 0.6 | 1.2 |
| 引擎错误 | 0 | 7 |

结论：两条路由 precision 都在 30% 左右，与 OCR 榜单各模型的 31-38% 同一量级；recall 在 8-10%，明显低于 OCR 的 13-20%，这是 diff-only 上下文的代价，也是 ContextRetriever（相关文件检索）未实现的直接证据。MiniMax 的 recall 和对照组误报都更高，token 是 DeepSeek 的 12 倍。中文自然度两条路由都接近满分，INV-334 的“翻译腔”问题在当前 prompt 下不显著，但 judge 本身是国产模型，这个分数需要人工抽查复核。

## 5. 后续

- recall 的主要缺口是上下文：建议下一步做相关文件检索（ContextRetriever），再用本评测集量化收益。
- 两条路由的 JSON 都带 `raw_findings`，`bench review --findings <report.json> --judge-route <route>` 可以不重跑模型只换 judge 或窗口重新评分。
- 评测集增量：新 case 加 `added_at` 后用 `--since` 单独跑。
