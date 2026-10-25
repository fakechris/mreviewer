# AGENTS.md — Involute Work Graph 绑定

本仓库已接入 [Involute](https://involute.lumenopen.com/) 作为项目状态与工作图内核（work graph）。Agent 在本仓库的一切长周期工作（里程碑、事项、阻塞、证据）以 Involute 为准，不写入本地 TODO 或临时笔记。

## 绑定信息

| 项 | 值 |
| --- | --- |
| Team key | `INV` |
| Project | **INV-319** `fakechris/mreviewer`（工作图唯一根节点） |
| Repository | `fakechris/mreviewer` |
| Web UI | https://involute.lumenopen.com/work/INV-319 |
| MCP | `https://involute.lumenopen.com/mcp`（Bearer `inv_agent_…`，token 存于 agent secret store，**严禁提交进 git**） |

## 三层拓扑（严格）

```
PROJECT  INV-319  fakechris/mreviewer
├── MILESTONE  INV-320  轻量部署与开源就绪基线（v0.20.0–v0.21.0）
│   ├── ISSUE  INV-321  SQLite 轻量部署模式            （Done）
│   ├── ISSUE  INV-322  Grafana 运营仪表板模板          （Done）
│   ├── ISSUE  INV-323  开源就绪包与发行链              （Done）
│   └── BUG    INV-1016 CI setup-go 1.21 低于 go.mod 1.25（P3，已立项）
├── MILESTONE  INV-324  结构化输出可靠性与国产模型生态
│   ├── ISSUE  INV-325  结构化输出探针工作流与实测矩阵    （Done）
│   ├── ISSUE  INV-326  GLM-5/Zhipu 集成与 tool_call 默认（Done）
│   └── ISSUE  INV-327  DeepSeek 等国产模型兼容性实测     （P1，In Progress）
├── MILESTONE  INV-328  多模型共识护城河
│   ├── ISSUE  INV-329  Anthropic compact schema 补回 anchor_snippet/evidence（修订提案待接受：降 P2，不再阻塞 INV-331）
│   ├── ISSUE  INV-330  ProviderResponse 多 provider 可观测性（P1，Ready；结构体已存在，缺消费方）
│   ├── ISSUE  INV-331  ConsensusReviewService 粗匹配共识（P2，BLOCKS → INV-332）
│   ├── ISSUE  INV-332  多模型语义去重（LLM-based）      （P2/v1.1，Ready；matcher 已实现，待接线）
│   └── ISSUE  INV-1021 单模型 N-pass 投票共识          （P2，Ready）
├── MILESTONE  INV-333  中文 Review 质量护城河
│   ├── ISSUE  INV-334  Review prompt 中文术语调优       （P1，Ready）
│   ├── ISSUE  INV-335  Severity 中文显示层映射          （P1，Ready）
│   └── ISSUE  INV-1022 Lens 式可插拔评审规则文件        （P3，Ready）
└── MILESTONE  INV-1017 评审可信度与质量基准（P2，Ready）
    ├── ISSUE  INV-1018 Finding 行号落点校验             （P2，Ready）
    ├── ISSUE  INV-1019 覆盖率证据与 incomplete verdict  （P2，Ready）
    ├── ISSUE  INV-1020 本地评测集与 LLM-as-judge bench   （P2，Ready）
    ├── ISSUE  INV-1290 复审收敛：增量复审/轮次上限/类别聚合（候选，P2）
    ├── ISSUE  INV-1291 Finding 反馈分类与噪声指标        （候选，P2）
    ├── ISSUE  INV-1292 变更风险分级与敏感路径            （候选，P3）
    └── BUG    INV-1351 PR 分支 ai-review.yaml 可关门禁/改路由（P2，已立项）
├── DECISION   INV-1352 面对 OpenCodeReview 的定位（竞争/接入/收窄）（候选，P2）
```

- 提案新工作必须先 `work_search` 查重；重复标题视为失败。
- 新节点一律带 `parent_id`（PROJECT → MILESTONE → ISSUE，禁止跨层或倒挂）。
- 计划外发现的工作（bugfix / 共享库修复）以 `DISCOVERED_DURING` 关联到当前工作项，不得直接塞进现有范围。

拓扑快照更新于 2026-10-24，详见 docs/plans/2026-10-07-roadmap-refresh.md。

## 工作流（claim 驱动）

1. `work_list_ready` / `work_search` → `work_get_context` 加载契约、祖先、阻塞、claim 与审计。
2. 用户选定后 `work_claim` 认领（不要一次租走整个队列）。
3. 本地规划与 grep 结果**不上传** Involute。
4. 过程中用 `run_report` 汇报阶段/阻塞/完成。
5. 扩大范围必须 `work_update`（带 `expected_revision`）或新建候选并 `DISCOVERED_DURING` 关联。

## 完成与证据规则

- **Run complete ≠ 工作验收**。Agent 永远不把工作置为 Done；完成只到 In Review，由人工验收。
- `work_commit`（候选 → 立项）与候选拒绝是**人工动作**；agent 提案后停下向用户汇报。
- 每个交付必须 `evidence_attach` 回填证据：PR 链接、测试结果（`go test ./...` 退出码 0）、验收文档（docs/acceptance/）。
- 结构化输出路由调整：先跑 `mreviewer structured-output-probe`，结论回填 docs/acceptance/ 探针矩阵，再改默认值。
- 生产代码修改必须绑定 INV-xxx：分支命名 `feat/INV-xxx-slug` 或 PR 标题带 `[INV-xxx]`。
