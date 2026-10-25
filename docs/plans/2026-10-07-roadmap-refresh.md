---
status: PROPOSED
supersedes: docs/designs/strategic-direction.md (部分前提)
---
# 2026-10-07 路线图刷新：OpenQodex 对比与业界动态

仓库最后一次功能提交是 2026-04-11（v0.1.13），之后只有 2026-09-10 的 README 徽章更新。
本文记录对 Involute 现有事项的核对结论、对 OpenQodex 源码的对比、2026 年 4 月至 10 月业界动态，
以及由此提出的新候选（已提交 Involute，等待人工 commit）。

## 1. 现有 Involute 事项核对（2026-10-07 源码核对）

| 事项 | Involute 状态 | 核对结论 | 动作 |
|---|---|---|---|
| INV-321/322/323 | Done | 与代码一致 | 无 |
| INV-325/326 | Done | 与代码一致 | 无 |
| INV-327 DeepSeek 实测 | In Progress | 仍有效。`DeepSeekCompatMode` 已存在，但 docs/acceptance 无 DeepSeek 小节 | 评论补充厂商现状 |
| INV-329 Anthropic compact schema | Ready, P0 | **前提过期**。compact schema 保留 canonical_key/symbol/old_line/new_line；semantic fingerprint 不受影响；真正缺的是 anchor_snippet 与 evidence | 已提交修订提案：收窄范围、降 P2、解除对 INV-331 的阻塞 |
| INV-330 多 provider 可观测性 | Ready | 部分过期。`ProviderResponse.SubProviderResults` 已存在，缺消费方 | 评论 |
| INV-331 粗匹配共识 | Ready | 仍有效，不应再被 INV-329 阻塞 | 随 INV-329 修订 |
| INV-332 LLM 语义去重 | Ready | 部分过期。`LLMSemanticMatcher` 已实现，未接线 | 评论 |
| INV-334/335 中文质量 | Ready | 仍有效，代码中无中文 severity 映射 | 无 |

另发现：CI 与 release workflow 的 setup-go 为 1.21，go.mod 要求 1.25（已以 bug INV-1016 立项）。

## 2. 战略前提变化

strategic-direction.md（2026-03-23）的三个市场前提需要修订：

- "GitLab Duo 太贵"：2026-03 起 GitLab 把 agentic code review 定价为每次 0.25 美元，18.10 起免费层也能用。价格不再是护城河，自托管、数据不出境、国产模型仍是。
- "PR-Agent 是最强开源对手"：PR-Agent 已从 Qodo 剥离给社区（The-PR-Agent 组织，Apache-2.0），商业版 Qodo 2.0 转向多 agent 架构。新的开源对手是 OpenQodex（扫描器 + 冻结快照 + 可证明完整性，push 前本地评审）。
- "多模型共识竞品无"：Cursor Bugbot 已公开 8 次并行采样加多数投票加 validator 模型的做法；SWR-Bench（FSE 2026）报告多评审聚合 F1 最多提升 43.67%。共识仍是差异点，但要从"多模型"扩展为"多采样 + 多模型"，并用基准量化。

业界共同趋势（2026 年 4 月至 10 月）：

1. **可信度优先于数量**：Bugbot 公布 80% 解决率，Greptile v4 降误报，Graphite 以低噪声定位。OpenQodex 把完整性做成退出码。
2. **规则文件标准化**：Copilot code review 读 REVIEW.md、AGENTS.md、CLAUDE.md；PR-Agent 默认注入 AGENTS.md 与 SKILL.md；Bugbot 用 .cursor/BUGBOT.md；CodeRabbit 支持 path 级 instructions。
3. **公开基准出现**：SWR-Bench、CR-Bench、c-CRAB、Kodus CodeReviewBench。研究显示 diff 超过 150 行时 F1 跌至 0.04 量级，截断策略直接影响质量。
4. **安全扫描与评审合流**：CodeRabbit Security Agent、Codex Security Cloud、Claude /security-review。
5. **国内**：通义灵码更名 Qoder CN 并内置 /code-review 与 /ultra-review；CodeBuddy 全线切 DeepSeek-V4-Pro 并内嵌 SAST。两者都绑定各自 IDE，不做 GitLab 自托管回写。
6. **国产模型结构化输出**：DeepSeek 一方 API 的 schema 强约束走 /beta 基址 tool strict 模式；GLM-5 系列 json_schema 仍静默失效；Kimi K2.7 与 Qwen3.5（百炼部分模型）支持 json_schema strict。

## 3. OpenQodex 对比要点

| 维度 | OpenQodex 0.8.1 | mreviewer |
|---|---|---|
| 触发点 | push 前本地，复用 Claude Code / Codex 登录 | push 后 PR/MR URL 或 webhook |
| 静态分析 | 13 个 pinned 扫描器 + tree-sitter 代码图 | 无 |
| 完整性 | 脚本可证：快照哈希、trace 越界、候选 disposition、hunk 覆盖 | 无覆盖率证据 |
| 行号校验 | finding 必须落在改动行或删除锚点 | 不校验，靠平台 API 报错 |
| 规则扩展 | 48 个 lens markdown 文件 | Go 常量专家包 |
| 多模型 / 国产模型 | 无 | 有 |
| 企业回写与控制面 | 无 | 有 |
| 中文 | 文体规则按英文分句，中文误判 | zh-CN 默认 |

## 4. 新候选（已提交 Involute）

| 候选 | 父节点 | 优先级 | 来源 |
|---|---|---|---|
| INV-1017 里程碑：评审可信度与质量基准 | INV-319 | P2 | OpenQodex + 基准论文 |
| INV-1018 Finding 行号落点校验 | INV-1017 | P2 | OpenQodex finalize |
| INV-1019 覆盖率证据与 incomplete verdict | INV-1017 | P2 | OpenQodex completion |
| INV-1020 本地评测集与 LLM-as-judge bench | INV-1017 | P2 | SWR-Bench / CR-Bench |
| INV-1021 单模型 N-pass 投票共识 | INV-328 | P2 | Cursor Bugbot |
| INV-1022 Lens 式规则文件 | INV-333 | P3 | OpenQodex lenses / Copilot REVIEW.md |
| INV-1016 CI go 版本 bug | INV-320 | P3 | 本地核对 |

## 5. 建议执行顺序

1. INV-1016（半小时）与 INV-329 修订接受。
2. INV-1020 评测集先行：没有它，INV-334、INV-331、INV-1021 都无法量化验收。
3. INV-1018、INV-1019 可信度底线，工程量小，直接改善平台回写成功率。
4. INV-327 扩展为 DeepSeek V4 + Kimi K2.7 + Qwen3.5 探针矩阵。
5. INV-331 粗匹配共识与 INV-1021 投票共识共用分组逻辑，一起做。
6. INV-1022 与 INV-334 合并推进中文 lens。

明确不做：本地 git diff 入口（OpenQodex 主场，与自托管企业定位不符）、扫描器层（工程量大，排在共识之后再议）。

## 6. 来源

- OpenQodex 源码 https://github.com/openqodex/openqodex （commit 549d330，2026-10-06）
- CodeRabbit changelog https://docs.coderabbit.ai/changelog
- Cursor Bugbot https://cursor.com/blog/building-bugbot
- GitHub Copilot code review changelog https://github.blog/changelog/2026-07-17-copilot-code-review-customization-and-configurability-improvements/
- GitLab 定价 https://about.gitlab.com/blog/agentic-code-reviews-with-flat-rate-pricing/
- PR-Agent https://github.com/The-PR-Agent/pr-agent/releases
- OpenAI DevDay 2026 https://openai.com/index/devday-2026-recap/
- SWR-Bench https://arxiv.org/abs/2509.01494 ；CR-Bench https://arxiv.org/abs/2603.11078 ；c-CRAB https://arxiv.org/html/2603.23448v2
- Qoder CN 更新 https://help.aliyun.com/zh/lingma/product-overview/qoder-cn-update-log ；CodeBuddy https://www.codebuddy.cn/docs/cli/release-notes/README
- DeepSeek API https://api-docs.deepseek.com/api/create-chat-completion/ ；Kimi https://platform.kimi.ai/docs/guide/response_format ；Qwen https://help.aliyun.com/zh/model-studio/qwen-structured-output

## 7. 2026-10-19 补充：Claude Code 团队评审流程博文的对照

来源：https://claude.dev/blog/solving-code-review-at-anthropic/ （2026-10-15）。核心数据：6 月到 9 月合并 PR 翻倍，revert 比例降 40% 以上；research preview 后有实质评论的 PR 从 16% 升到 54%；收敛改造后一轮通过率 65% 到 78%，平均轮次 2.08 到 1.32。

| 博文做法 | mreviewer 现状（2026-10-19 核对） | 动作 |
|---|---|---|
| 多 agent 并行找 bug，独立 verifier 投票后才发布 | INV-1021 只有 N-pass 投票 | 评论 INV-1021，预留 verifier 阶段 |
| finding 标 Important / Nit / Pre-existing | schema 有 introduced_by_this_change，渲染层未用 | 评论 INV-335；新候选“反馈分类”落库与 verdict 开关 |
| 每条 finding 必须 fix 或带理由 resolve | gate_mode threads_resolved 已有；ignore 不记原因 | 新候选“Finding 反馈分类与噪声指标” |
| 后续 commit 修了就自动清除 | writer 自动 resolve 已有 | 无 |
| 轮次上限、diff-only 复审、按类别提示、不重复 | IncrementalState 是死代码，无轮次计数 | 新候选“复审收敛” |
| 风险分级决定人工审批数，敏感路径强制人工 | gate 无风险概念，无敏感路径配置 | 新候选“变更风险分级” |
| incident / revert 变 eval，每天跑 | INV-1020 评测集为静态 | 评论 INV-1020，改为可增量追加 |
| 按人对评论的处置统计噪声 | admin trends 只有数量 | 并入“反馈分类”候选 |
| REVIEW.md 由 owner 写，短、列 skip 路径、要求文件行证据 | rules.Loader 已支持 REVIEW.md | 无，INV-1022 lens 补充 |
| 安全评审单独一遍并可阻断 | security pack 与 bug 同一次调用 | 暂不拆分，待 INV-1020 数据 |

明确不做：nightly 全库扫描、merge queue、feature flag 与发布通道，这些属于 CI/CD 与发布系统，不是 review 工具的范围。

执行顺序调整：复审收敛候选应排在 INV-1018/1019 之后、INV-331 之前，因为它直接降低每个 MR 的 token 成本，是共识功能上线前的成本前提。

## 8. 2026-10-24 补充：OpenCodeReview 核实与 7 到 10 月工具调研的吸收

### 8.1 OpenCodeReview（alibaba/open-code-review）核实结论

Apache-2.0，Go 实现，GitHub 显示约 48k stars，README 带简体中文。源码核实（commit 2026-10-24 克隆）：

- 架构：确定性代码负责文件选择、规则匹配、评论定位；agent 负责读全文件与调用关系。CLI 与 CI 集成（GitHub Actions、GitLab CI、Gerrit），VSCode 扩展，Claude Code / Codex / Cursor / Kimi Code 插件。输出 text / json / sarif。
- 扩展点只有四个：`rule.json`（按 glob 的自然语言规则，可引用 .md/.txt 文件，`merge_system_rule`），`--background` 业务上下文，Delegation Mode（`ocr delegate preview/rule` 只给范围与规则，推理交宿主 agent），MCP 客户端配置（消费外部上下文工具，不是对外 server）。prompt 模板编译进二进制（internal/config/template 的 templateFS），没有用户覆盖入口。
- 没有的东西：服务端、webhook、历史与 finding 状态机、discussion 回写、admin、可观测。Roadmap 明确不做自动修复与通用 coding assistant，H1 2027 计划长期记忆。
- 自报偏 precision，token 约为通用 agent 的 1/9，有 200 PR / 1505 标注的 benchmark。

**对战略前提的影响**：strategic-direction.md 的"中国市场没有直接竞品"不再成立。已提 DECISION 候选 INV-1352（竞争 / 接入为引擎 / 收窄为服务层）。

**"能否接入自定义 audit / review skill"的回答**：不能以 skill 形式接入引擎内部。能做的是三条路：把 skill 的检查清单写成 `rule.json` 的 rule 文本或引用的 .md 文件，按路径匹配注入 prompt；用 Delegation Mode 让宿主 agent 带着任意 skill 做推理，OCR 只负责范围与规则；用 `ocr scan` 做全文件审计后再喂给自己的 skill。要改审查流程本身，只能改源码重编。

### 8.2 7 到 10 月工具调研的吸收

| 研究中的模式 | 吸收方式 |
|---|---|
| 评估方法论：20 到 30 个历史 PR 加易误报重构对照组，四指标 | INV-1020 修订提案（待接受） |
| PR-Agent v0.48 把敏感 provider 配置限制在宿主 | 发现同类缺口，bug INV-1351：PR 分支 ai-review.yaml 可关门禁、改路由与阈值 |
| fp-check 的"危险但受保护"裁定工序 | 对应 INV-1021 的 verifier 阶段，不是 INV-331/332 去重 |
| ln-56 "已接受约束 vs 推断边界" | 建议 finding 增加依据来源字段，并入 INV-1022 或 INV-334 |
| Trail of Bits regression-hunt / patching-bugs 三 worktree 验证 | 架构级决定，当前不做；INV-1021 verifier 先只读不执行 |
| Mantis 预算上限与断点续跑 | 每 MR token 预算可作为 INV-1290 追加验收 |
| Addy Osmani mutation 实验验证测试有效性 | 需执行能力，与上一条同归 |
| OCR 的确定性相关文件检索 | mreviewer 的 ContextRetriever 接口未实现，是实际缺口；等 INV-1020 数据判断漏报有多少来自缺上下文后再立项 |

来源：alibaba/open-code-review 源码；The-PR-Agent/pr-agent v0.48.0 release；trailofbits/skills 2026-10-20 提交；akkie76/code-review-skills 1.0.0；google/mantis；levnikolaevich/claude-code-skills ln-56 2026-10-18 提交。
