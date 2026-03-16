# 自托管 GitLab MR 自动代码审查系统：扩范围、重证据、可落地调研

更新时间：2026-03-16

## 0. 执行摘要

目标系统不是“在 MR 下发一条总评评论”的轻量 bot，而是一个面向 self-managed GitLab 的、可跨 group / project 扩展的自动代码审查平台。它应当具备：

- 平台级事件接入：System Hook / Group Hook / Project Hook / CI 触发并存。
- 中央审查编排：获取 MR 版本、diff、上下文、规则文件、历史讨论。
- 外部 LLM 适配：对 OpenAI / Anthropic / Azure OpenAI / Bedrock / Vertex 等进行统一封装。
- 严格结构化输出：模型只返回 JSON schema；系统负责定位、去重、幂等、回写。
- GitLab 原生交互：以 diff discussion/thread 为主，支持 suggestions、resolve、reopen、过期/失效处理。
- 生命周期管理：fingerprint、dedupe、rerun、历史 run 关联、merge gate 集成。
- 安全与治理：代码外发边界、敏感项目策略、token 最小权限、审计、可观测性。
- 多租户 / 多项目能力：实例级或 group 级覆盖，集中配置与仓库内局部规则并行。

### 最终建议

推荐主方案为：

**A. 纯 webhook service 架构（以 System Hook 为首选入口）**

并补充两类可选适配器：

1. **GitLab diff discussion writer** 作为主输出通道。
2. **可选 merge gate 适配器**：
   - 通用：依赖 GitLab 的 “All threads must be resolved”。
   - Ultimate 可选：外部 status check，仅用于快速/补充 gate，不作为深度 LLM 审查的唯一门禁。

不建议直接 fork 某个单仓库 reviewer 项目作为底座。原因不是“功能少”这么简单，而是这些项目普遍缺少以下平台能力：

- 跨项目/跨 group 统一接入与治理。
- MR 版本级状态机和 durable dedupe。
- 严格的 comment anchor 重定位与失败兜底。
- 规则继承链（平台默认 / group / project / repo / directory）。
- 与 GitLab merge checks、threads resolve、status checks 的组合设计。
- 生产级安全、审计、可观测性、重试与成本控制。

---

## 1. 市场与商业参考资料综述

本节按“运行模式、可借鉴点、权限边界、输出协议、限制条件”拆解。

### 1.1 OpenAI Codex GitLab cookbook / Codex SDK

#### 运行模式

- **GitLab CI / Jenkins 中运行 CLI 或 SDK**。
- 典型链路：CI 里拉取 diff -> 调 Codex headless / SDK -> 返回结构化 findings -> 再调用 SCM API 写评论。
- 另一个 cookbook 侧重 **输出 GitLab Code Quality artifact**，让结果出现在 MR 小组件与 changes 视图。

#### 可借鉴的架构思想

- **强约束 JSON schema**：不是让模型直接写 Markdown，而是让模型输出 findings 数组，然后系统自己决定如何回写 GitLab。
- **解析失败兜底**：从日志中提取 JSON、验证 schema、失败则退回 `[]` 或 summary，而不是把坏格式原样发到 MR。
- **allowlist + 最小上下文**：先构建 file allowlist，再把必要上下文送给模型，减少泄漏和成本。
- **输出适配器分层**：模型输出和 GitLab 投递协议解耦；同一 findings 可转成 inline comments，也可转成 Code Quality JSON。

#### 权限边界

- 运行在 CI runner 内时，权限边界更贴近项目；可使用 CI 提供的 MR 环境变量。
- OpenAI 文档还强调了一个很值得借鉴的安全点：**让 agent 进程无法轻易回读自己的 API 密钥或过高权限凭据**。

#### 输出协议

- 第一类：自定义 JSON schema。
- 第二类：GitLab Code Quality（CodeClimate 子集）JSON artifact。

#### 限制条件

- CI-only 模式天然依赖每个项目或模板纳管。
- Code Quality widget 虽然适合汇总与 widget 展示，但不天然支持“讨论—回复—resolve—重跑去重”的 review 生命周期。

### 1.2 Claude Code：GitLab CI/CD 与 Code Review

#### 运行模式

Anthropic 相关资料实际分成两种：

1. **Claude Code GitLab CI/CD**：你自己的 GitLab runner 中运行 agent/CLI。属于 **CI job 驱动**。
2. **Claude Code Review（研究预览）**：Anthropic 托管的多 agent PR review。当前资料主要围绕 GitHub PR，不是 GitLab MR 原生产品。

#### 可借鉴的架构思想

- **多 agent + verification**：把“发现候选问题”和“验证该问题是否真实”拆开，有助于降误报。
- **severity + confidence 排序**：不是把所有问题平铺出来，而是按影响度和可信度排序。
- **规则文件分层**：`CLAUDE.md` 适合全局工程上下文，`REVIEW.md` 适合 review-only 规则。
- **事件驱动 CI**：通过 mention / comment / pipeline 触发不同任务，形成“自动 review + 手动复审”的混合控制面。

#### 权限边界

- GitLab CI/CD 模式强调：任务运行在隔离 job 中，走你自己的 runner、分支保护、审批流。
- 支持 Claude API、Bedrock、Vertex AI；其中 **OIDC / WIF 方式** 对长期静态密钥的替代非常值得照抄。

#### 输出协议

- Claude Code Review 产品层偏“托管 inline comment + summary”。
- GitLab CI/CD 自建模式更像“agent CLI + 你自己的 writer”。

#### 限制条件

- Claude 托管 Review 当前主要面向 GitHub PR。
- 深度 review 平均时延较长，不适合直接套到 GitLab 的短超时外部 status check 作为唯一门禁。

### 1.3 CodeRabbit（GitLab 与 self-managed GitLab）

#### 运行模式

CodeRabbit 对本题最值得研究，因为它已经覆盖 GitLab / self-managed GitLab：

- **自托管 GitLab 接入**：以 **dedicated GitLab bot user + webhook endpoint + Docker 部署** 为主。
- 提供脚本把 webhook 加到单项目或 group 下所有项目。
- 这是典型的 **central webhook service** 模式。

#### 可借鉴的架构思想

- **增量 review**：首次全量 review；新 commit 只 review 新变化，避免重复评论。
- **不重复已解决问题**：resolved comments 不再反复刷屏。
- **central configuration + repository override + path instructions**：非常适合多项目平台治理。
- **命令控制面**：如 pause / resume / resolve / manual review，说明评论系统不只是输出，还要有“控制协议”。
- **知识/规则来源融合**：可自动读取 `CLAUDE.md`、`AGENTS.md` 等现有规则文件。

#### 权限边界

- GitLab 侧通常需要 dedicated 用户 + PAT（`api` scope）+ 至少 Developer 级项目访问。
- 说明自托管 GitLab 场景下，“独立 bot 身份”比复用单个管理员 token 更可控。

#### 输出协议

- 以 MR 评论与线程交互为主。
- 同时支持 repo 级 YAML 配置和跨 repo 配置继承。

#### 限制条件

- 自托管 GitLab是 Enterprise/高 seat 数方案的一部分，很多体验来自成熟 SaaS 控制面与长期积累。
- 它的“知识库、多 repo 分析、组织级 learnings”等能力复制成本高，不建议纳入 MVP。

### 1.4 GitLab Duo / Duo Agent Platform

#### 运行模式

- GitLab 自家产品，深度嵌入 MR review UX。
- 新一代 Code Review Flow 已建立在 Duo Agent Platform 之上。

#### 可借鉴的架构思想

- **把 repo 规则显式化**：`.gitlab/duo/mr-review-instructions.yaml` 使用 `fileFilters` 精确约束规则作用范围。
- **additive instructions**：自定义规则是附加到默认 review 标准上，而不是全量替换。
- **把 MR 元信息、变更前文件、diff、文件名、用户自定义说明统一送模**。

#### 权限边界

- GitLab 原生产品有天然 UI/权限集成优势，这部分很难在自研里等价复制。

#### 输出协议

- 原生 MR review 流与 GitLab 内部 agent 平台契合，非公开底座不适合作为自研复用对象。

#### 限制条件

- 很多能力依赖 GitLab 的产品内建服务和 Agent Platform / AI Gateway 体系。
- 适合作为“交互与规则设计参考”，不适合作为实现底座参考。

### 1.5 Gemini Code Assist 及 GitLab 相关公开实现

#### 运行模式

- **官方 review 产品当前重点是 GitHub PR review**。
- GitLab 方向公开资料更多是 **Google Codelab / GenAI automation demo / Developer Connect + GitLab 连接**，属于参考实现而非成熟产品模式。

#### 可借鉴的架构思想

- **style guide / config 文件** 设计值得参考：仓库内配置覆盖组织级默认。
- **Developer Connect / GitLab Connector / MCP** 提供了一种“不是直接装在 GitLab 里，而是通过连接器取上下文”的思路。

#### 限制条件

- 对 GitLab MR 自动 review 而言，Gemini 目前更像“模型提供方 + DIY 教程”，不是现成的产品路线。
- 因此它适合作为 LLM 供应商与连接器模式参考，不适合当作最主要的 GitLab 产品形态 benchmark。

### 1.6 Devin Review 与 REVIEW.md 机制

#### 运行模式

- Devin Review 当前公开资料仍以 GitHub PR 为主。
- 但其 **REVIEW.md / AGENTS.md / 目录级规则文件** 机制非常值得借鉴。

#### 可借鉴的架构思想

- **REVIEW.md 专用于 review，不污染 general agent memory**。
- **目录级规则就近生效**：适合 monorepo / 多语言 / 分层服务。
- **mark resolved / follow-up chat / auto-fix** 说明 review 生命周期应是可交互、可关闭、可追踪的。

#### 限制条件

- Devin 的托管体验、内置 diff UI、Auto-Fix 闭环带有明显的产品平台属性。
- 自研时应抽取其“规则文件 + review 生命周期”思想，而不是模仿其整套 hosted review 体验。

---

## 2. 竞品能力拆解：最值得照抄的做法 vs 不适合自研照搬的部分

### 2.1 值得直接照抄的做法

1. **模型只产出严格结构化 findings，不直接产出最终评论文本协议**。
2. **增量 review + 不重复 resolved comment**。
3. **review 专用规则文件 `REVIEW.md` + 通用上下文文件 `CLAUDE.md` / `AGENTS.md` 并存**。
4. **配置继承链**：平台默认 -> group -> project -> repo config -> path-level rules。
5. **把 inline discussions 和 merge gate 解耦**：
   - 讨论负责 developer UX；
   - gate 负责 merge policy；
   - 两者不要混成单一机制。
6. **输出适配器模式**：同一 findings 可以写 diff discussion、summary note、Code Quality、RDJSON/SARIF adapter。
7. **供应商抽象层**：OpenAI / Anthropic / Azure OpenAI / Bedrock / Vertex 可替换。
8. **人工控制面**：rerun、resolve、ignore、path focus、only security 等命令。

### 2.2 不要在 MVP 照搬的部分

1. **长期知识库 / 跨仓库向量索引 / 历史记忆学习**。
2. **复杂多 agent 编排**。
3. **托管式 Web 产品控制台的大量配套能力**（账单、组织管理、分析面板、学习偏好、全局 KB）。
4. **把“自动修复”混入首版范围**。先把“发现 + 定位 + 去重 + 生命周期”做扎实。
5. **把外部 status check 当成深度 LLM 审查的唯一 gate**。超时与项目级配置限制会让落地很脆。

---

## 3. 开源项目对比：可借鉴模块 vs 不适合直接做底座

### 3.1 dify-gitlab-mr-reviewer

#### 适合借鉴

- 事件接收与工作流分离。
- 外部 LLM / workflow 编排思路。
- Docker 化部署和基础日志监控。

#### 不适合直接做底座

- 偏 Dify workflow 驱动，不是围绕 GitLab review 生命周期设计。
- 缺少 durable dedupe、discussion 级 state、版本化 rerun 策略。
- 难承载多项目、多 group、平台级策略治理。

### 3.2 AICodeReview

#### 适合借鉴

- `.aiignore` 一类忽略文件思路。
- 极简多项目参数化经验。

#### 不适合直接做底座

- 脚本型实现，状态与幂等能力弱。
- 不具备讨论线程生命周期、结构化 schema、重试/审计/可观测性。

### 3.3 ai-mr-review / ai-gitlab-code-review 一类项目

#### 适合借鉴

- 最小 API client、prompt 原型。
- “拿 diff -> 产出评论”的基本串联。

#### 不适合直接做底座

- 常见问题：
  - 直接把整份 diff 拼 prompt；
  - 输出 Markdown 或松散 JSON；
  - 不做 anchor 校验与回退；
  - 不做 dedupe、resolve、rerun。
- 更像 PoC，而不是平台底座。

### 3.4 gitlab-mr-reviewer（FastAPI / Spring Boot 等实现）

#### 适合借鉴

- Webhook service 入口、模块划分（context generator / analyzer / notifier）。
- repo-specific prompts 的集中管理。

#### 不适合直接做底座

- 往往绑定单一模型/云服务。
- 生命周期与数据模型仍然偏薄。
- 对多租户、权限边界、merge gate 组合、错误恢复覆盖不足。

### 3.5 reviewdog

#### 适合借鉴

- **作为输出适配器非常优秀**：
  - 支持 `gitlab-mr-discussion`；
  - 支持 suggestions；
  - 支持 RDJSON / RDJSONL / SARIF 等多种上游格式。
- 如果组织里已有大量 linter / scanner 输出，可以把它作为统一 comment writer。

#### 不适合直接做底座

- reviewdog 是“诊断结果投递器”，不是“MR 自动审查平台”。
- 它不负责：
  - 统一事件编排；
  - 历史 finding 去重；
  - discussion resolve / stale / superseded 生命周期；
  - LLM prompt、schema、解析和风控。

### 3.6 danger-js

#### 适合借鉴

- 适合 **CI 中的轻量规则检查、summary 评论、流程提醒**。
- 适合做非 LLM 的 policy bot。

#### 不适合直接做底座

- Danger 的心智模型是“在 CI 跑 arbitrary checks，然后发一条或少量评论”。
- 不适合作为 per-finding、可解析、可去重、可重定位的 LLM review 主链路。

### 3.7 codebleu 为什么不属于 GitLab MR 自动 review 主链路

CodeBLEU 是 **代码生成/翻译质量的离线评估指标**，关注 n-gram、AST、data-flow 等相似性。它不是：

- 事件触发机制；
- MR diff 定位协议；
- 评论投递协议；
- discussion 生命周期系统；
- merge gate 机制。

因此 CodeBLEU 最多可用于：

- 离线评估“模型提出的修复建议”质量；
- 对比不同 prompt / 模型版本的 patch-like 输出。

它不属于 GitLab MR 自动 review 的主链路方案。

---

## 4. GitLab 能力边界分析

### 4.1 触发机制：System Hook / Group Hook / Project Hook / CI

#### 结论

- **平台级首选：System Hook**。
- **组织级备选：Group Hook**。
- **通用兜底：Project Hook**。
- **安全边界优先或 repo 自治优先时：CI 驱动**。

#### 原因拆解

##### A. System Hook

适合：

- self-managed GitLab；
- 有管理员权限；
- 希望覆盖全实例或多个 group/project；
- 想减少 webhook 配置漂移；
- 需要平台型集中编排。

优点：

- 覆盖全实例；
- 触发 merge_request 事件；
- 适合统一 allowlist / denylist；
- 最接近商业平台产品形态。

代价：

- 需要实例管理员介入；
- ingress 服务必须自己做项目过滤、授权与降噪。

##### B. Group Hook

适合：

- 没有实例管理员权限，但有 group Owner；
- 想按组织/业务单元集中管理；
- Premium / Ultimate 环境。

优点：

- 覆盖该 group 及 subgroups / projects；
- 比逐项目配置更可控。

代价：

- 不是 Free 通用能力；
- 多 group 时仍需多份管理。

##### C. Project Hook

适合：

- Free 版；
- 单项目或少量项目试点；
- 没有 group / system 级权限。

优点：

- 通用、简单；
- 容易按项目渐进 rollout。

代价：

- 规模化运维成本高；
- 容易出现配置不一致。

##### D. GitLab CI 内触发

适合：

- 强调“代码尽量不离开项目 runner 边界”；
- 团队偏好 repo-local config-as-code；
- 希望直接复用 pipeline 作为 merge gate；
- 需要 OIDC/WIF 对接云模型而不发放长期静态密钥。

缺点：

- 需要每个 repo 纳管；
- 运行成本转移到 runner/pipeline；
- fork MR / secret / protected runner 风险更高；
- 中央 dedupe / 跨项目治理更难。

##### E. File Hook

理论上可行，但不推荐作为主方案：

- 需要直接落在 GitLab 服务器文件系统；
- 运维侵入强；
- 版本升级与多节点部署负担大；
- 没有明显胜过 System Hook 的产品价值。

### 4.2 评论写回：discussion / inline note / general note / check report

#### 优先级建议

1. **Diff discussion / thread（首选）**
2. **File-level discussion（无法稳定定位到行时的次优）**
3. **General MR note（仅兜底）**
4. **Check report / widget（仅补充，不作为逐 bug 主通道）**

#### 原因

##### Diff discussion / thread

优点：

- 出现在 Changes 视图，开发者接受度最高。
- 可 resolve / reopen。
- 可 reply，天然支持后续对话。
- 可带 suggestions。
- 能参与 “All threads must be resolved” merge check。

这是“每个 bug 逐条 append 回 MR”的最自然落点。

##### File-level discussion

适合作为 anchor 无法精确到行的兜底，例如：

- 文件整体设计问题；
- 新增文件但 diff hunk 已塌缩 / 过大 / line 不稳定；
- 解析失败但仍能确定文件。

##### General note

只在以下情况使用：

- 完全无法锚定文件/行；
- schema 解析失败；
- 模型只给出 run-level 诊断或需要单条 summary。

##### Check report / widget

优点：

- 适合展示 run 级状态、汇总指标、非对话式结果。
- Code Quality 对 fingerprint 有天然聚合能力。

缺点：

- 不适合讨论、resolve、follow-up。
- status check 还是项目级配置，且 pending 超时短。
- 不适合承载“逐 bug 生命周期”。

### 4.3 GitLab merge checks 边界

#### 可用组合

- **所有版本通用且最稳**：`All threads must be resolved` + bot 发 resolvable diff threads。
- **CI 架构天然可用**：pipeline job 成功/失败作为 gate。
- **Ultimate 可选补充**：external status checks。

#### 重要限制

external status checks 的几个现实限制：

- **项目级配置，不是多项目共享对象**；
- **pending 超时约 2 分钟**；
- 对长耗时 LLM review 不友好；
- 更适合作为快速 policy check 或补充 gate，而不是深度审查唯一门禁。

因此：

- 如果 review 常常 >2 分钟，**不要把 status check 作为唯一门禁**。
- 更推荐把 status check 作为 run-level 摘要或快速预检，真正阻止 merge 的主机制还是 unresolved discussions 或 CI job。

### 4.4 Diff / 上下文获取能力边界

需要注意几件 GitLab API 细节：

- MR 创建后，`diff_refs` / `changes_count` 可能异步填充，不能假设 webhook 一到就能立即拿到完整 diff。
- 超大 MR 的 `changes_count` 可能是字符串并出现 `1000+`。
- `/diffs` 会返回 `collapsed`、`too_large`、`generated_file` 等信号，应该用于上下文裁剪和降级策略。
- 写 diff discussion 前，应先获取 **最新 MR version** 的 `base/start/head` 三个 SHA。
- `patch_id_sha` 可作为“语义上相同 patch”的辅助判断，用于去重和 skip rerun。

### 4.5 结果投递协议：reviewdog / SARIF / CodeClimate / 自定义 JSON schema

#### 建议结论

- **内部主格式：自定义严格 JSON schema。**
- **对外适配格式：**
  - GitLab discussion writer（首要）
  - 可选 RDJSON / SARIF adapter（兼容 reviewdog 或既有工具链）
  - 可选 CodeClimate JSON（CI 中上报 Code Quality widget）

#### 为什么不是直接以 SARIF 或 CodeClimate 作为主格式

SARIF / CodeClimate 更适合“静态分析诊断”，但 LLM review 还有额外需求：

- 规则来源（自然语言规则、repo rule file）
- 置信度
- issue category / review-only metadata
- 文件级 / 行级 / 无法定位三种 anchor 状态
- 建议 patch / suggestions
- canonical fingerprint ingredients
- stale / superseded / resolved lifecycle

这些都更适合先用内部 JSON schema 表达，再转换到外部协议。

### 4.6 仓库级规则文件设计

建议同时支持两类文件：

#### A. `REVIEW.md`

- 面向自然语言 review 规则。
- 可 root + directory scoped。
- 典型内容：
  - 总是检查什么
  - 不要检查什么
  - 某目录下的特殊规范
  - 哪些警告属于 intentional design

#### B. `.gitlab/ai-review.yaml`

- 面向机器配置。
- 典型字段：
  - include / exclude paths
  - generated / vendor / binary policy
  - provider route
  - severity threshold
  - confidence threshold
  - merge gate mode
  - context mode / token budget
  - enable/disable rule packs

#### 文件读取优先级建议

1. 平台默认配置
2. group policy
3. project policy
4. repo `.gitlab/ai-review.yaml`
5. 根目录 `REVIEW.md`
6. 受影响路径上最近的 `REVIEW.md`
7. 可选读取 `CLAUDE.md` / `AGENTS.md`（默认关闭或 allowlist 开启）

---

## 5. 核心问题逐项回答

### 5.1 self-managed GitLab 最稳的触发机制是什么？

**答案：System Hook 是首选。**

#### 选择顺序

1. **System Hook**：有 admin 权限、要跨多 group/project 扩展时最稳。
2. **Group Hook**：没有 admin、但想在一个组织范围纳管时最合适。
3. **Project Hook**：最通用的 fallback，适合 Free / 小范围试点。
4. **GitLab CI 触发**：适合代码外发边界严格、repo 自治更重要的场景。

#### 具体边界条件

- 平台团队主导、要统一观测/配置/去重：选 System Hook。
- 某个 BU/大组独立治理：选 Group Hook。
- 单项目试点或无高权限：选 Project Hook。
- 不希望中央服务直接拉代码，且更信任 runner 边界：选 CI。

### 5.2 “每个 bug 逐条 append 回 MR”优先选什么？

**优先选 GitLab diff discussion / thread。**

因为它同时满足：

- inline 定位；
- 可 resolve；
- 可 reply；
- 可 suggestions；
- 能接入 threads-resolved merge gate。

如果不能稳定落到某行：

- 第二选择 file-level discussion。
- 最后才是 general note。

check report / code quality widget 只做辅助摘要，不做主投递通道。

### 5.3 LLM 输出最适合的中间格式是什么？

**答案：自定义严格 JSON schema。**

示例字段：

- `schema_version`
- `run_summary`
- `findings[]`
  - `title`
  - `body_markdown`
  - `severity`
  - `confidence`
  - `category`
  - `path`
  - `anchor.kind` (`line` / `range` / `file` / `general`)
  - `anchor.old_line` / `anchor.new_line`
  - `anchor.snippet`
  - `evidence`
  - `suggested_patch`
  - `rule_refs`
  - `canonical_key`

#### 解析失败兜底

1. 首选模型原生 structured output / JSON schema。
2. 失败则做 JSON substring extraction。
3. 再失败可做一轮 tolerant repair。
4. 仍失败则：
   - 该 run 标记 `parser_error`；
   - 发 1 条 general summary note / status；
   - 不发损坏的 inline comments。

### 5.4 如何映射为文件/行评论？

推荐顺序：

1. 获取最新 MR version 的 `base/start/head`。
2. 校验 finding 的 path 是否仍在当前 diff 中。
3. 用 hunk map 校验 line 是否可落位。
4. 若 line 失效但文件仍有效，尝试用 snippet / semantic anchor 重定位。
5. 仍失败则退化为 file-level discussion。
6. 再失败则退化为 general note。

### 5.5 如何做 fingerprint / dedupe / rerun？

建议用 **双层 identity**：

#### A. Run idempotency key

用于避免同一个 webhook 或同一个 MR HEAD 被重复处理：

`instance_id + project_id + mr_iid + head_sha + trigger_type`

#### B. Finding fingerprint

由系统生成，不信任模型原样输出，组成建议：

- 规范化 path
- 规范化 issue category / rule id
- symbol / function / class
- anchor snippet hash
- 归一化 title template
- 可选 evidence key

可同时保留：

- `anchor_fingerprint`：定位稳定时去重强。
- `semantic_fingerprint`：rebase / line 移动时帮助识别同一问题。

#### 生命周期建议

- 当前 run 再次发现同一 active finding：不重复发 comment，只更新 `last_seen_run_id`。
- 发现相同 semantic finding 但 line 变了：
  - 若可移动，更新 discussion；
  - 若 GitLab 不支持迁移，则新建 discussion 并把旧 discussion resolve 为 superseded。
- 新 run 没再发现某 finding：
  - 标记 `fixed` 或 `stale`；
  - 按策略自动 resolve bot discussion，或仅在系统内关闭。

### 5.6 如何控制代码泄漏、token 权限、prompt 注入、恶意 diff、超大 MR、误报成本？

#### 代码泄漏控制

- 默认只发 **changed hunks + 少量邻近上下文**。
- 大文件 / 二进制 / generated / vendor / lock files 默认不发。
- 支持项目级“禁止外发代码”与“仅允许某些 provider”。
- 对高敏仓库可走内部模型或直接禁用 LLM review。

#### token 权限

- webhook secret 与 GitLab bot token 分离。
- 使用 dedicated bot user。
- PAT / Project Access Token 只授予 `api` 必需权限，尽量以 Developer 角色覆盖目标项目。
- 云模型优先 OIDC / STS / WIF，避免长期静态云凭据。

#### prompt 注入与恶意 diff

- 明确把 repo 内容视为 **不可信输入**。
- 只有 allowlist 的规则文件（如 `REVIEW.md`）才进入“可信规则层”；普通代码/README 只做证据，不做指令源。
- agent 不执行仓库代码；MVP 不开放 shell/tool use 给模型。
- 对包含“忽略此前系统提示”“泄露密钥”之类文本的 diff 做 sanitization 标记。

#### 超大 MR

- 按文件与 hunk 分批。
- 设置 `max_files`、`max_changed_lines`、`max_tokens`。
- 超阈值进入降级模式：
  - 仅高风险文件；
  - 仅 summary；
  - 或提示“请手动拆分 MR”。

#### 误报成本控制

- 只自动发布 `confidence >= threshold` 的 findings。
- 支持 `blocking severities` 与 `nit` 分层。
- 可选 verification pass。
- 记录 reviewer feedback，供后续规则/提示优化。

### 5.7 商业产品中哪些最值得照抄，哪些复制成本过高？

#### 最值得照抄

- OpenAI：schema-first + adapter pattern。
- Claude：多 agent / verification 思想、`REVIEW.md` 分层、OIDC 安全思路。
- CodeRabbit：incremental review、resolved comment suppression、central config、path rules、命令控制面。
- GitLab Duo：repo 内 YAML 指令文件 + additive custom instructions。
- Devin：directory-scoped review rules、review lifecycle 与 resolved 语义。

#### 复制成本高且不适合 MVP

- SaaS 长期知识库与 learnings。
- 大规模多 agent 编排。
- 内建 Web UI / 分析面板 / 组织控制台。
- hosted app / marketplace / enterprise identity plumbing。

---

## 6. 三套候选架构

### 6.1 A. 纯 webhook service 架构

#### 数据流

1. GitLab System/Group/Project Hook -> Ingress
2. Ingress 归一化事件并写入 `hook_events`
3. 调度器生成 `review_run`
4. Worker 拉取 MR version / diffs / context / rules
5. 调用 LLM provider
6. 解析 schema -> 生成 findings
7. Dedupe engine 与历史 findings 比对
8. Comment writer 直接调用 GitLab Discussions API 回写
9. 可选：发布 run summary / status / metrics

#### 优点

- 最贴合多项目、多 group 的平台化目标。
- 中央 dedupe / rerun / config / observability 最好做。
- 不依赖每个项目配置 pipeline。
- 可避免执行仓库代码，安全边界优于 CI-only。

#### 缺点

- 需要自建可靠 webhook、队列、状态库、comment writer。
- anchor 定位、讨论生命周期、权限治理都要自己做全。

#### 复杂度

中高。

#### 可靠性

高，前提是有持久队列、幂等与 outbox。

#### 评论质量

高。因为中央服务更容易获取历史讨论、规则、跨 run 状态。

#### 扩展性

高。

#### 安全性

高。可做到“只读 GitLab API + 不执行 repo 代码 + 最小外发”。

#### 对 self-managed GitLab 适配难度

中。需要管理员接入 System Hook，但一旦接入后长期收益最高。

### 6.2 B. GitLab CI job 驱动架构

#### 数据流

1. `merge_request_event` pipeline 触发
2. Job 在 runner 中获取 MR diff 与上下文
3. Job 调 LLM
4. Job 直接写 comments，或产出 Code Quality / RDJSON / summary
5. Job status 作为 merge gate

#### 优点

- 更贴近 repo / runner 边界，平台侵入小。
- 天然融入 pipeline gate。
- 很适合 OIDC 对接云 provider。
- setup 可以来自 group CI template。

#### 缺点

- 纳管与配置在 repo 维度扩散。
- 中央 dedupe / 历史状态 / 多项目治理更难。
- fork MR / secret / runner 风险更高。
- pipeline latency 和 runner 成本更明显。

#### 复杂度

中。

#### 可靠性

中。依赖每个项目 CI 健康度与 runner 可用性。

#### 评论质量

中高。上下文可更丰富，但不同项目一致性差。

#### 扩展性

中。

#### 安全性

中。若执行 clone / tooling 更容易与不可信代码混在一起。

#### 对 self-managed GitLab 适配难度

低到中。对于已有 CI 体系的团队易落地。

### 6.3 C. webhook service + reviewdog / comment writer 混合架构

#### 数据流

- 中央 webhook/orchestrator 负责事件、状态、LLM、dedupe。
- findings 转成 RDJSON / SARIF / internal JSON。
- 由 reviewdog 或定制 writer 负责回写 GitLab。

#### 优点

- 如果已有 reviewdog/linter 生态，可复用成熟 reporter。
- 易于把静态分析与 LLM finding 接到同一“诊断总线”。

#### 缺点

- 架构会分成“中央状态系统”和“诊断投递器”两层，复杂度上升。
- reviewdog 本身不理解 LLM finding 生命周期，仍要自建 dedupe/state。
- 出问题时排障链条更长。

#### 复杂度

高。

#### 可靠性

中高，取决于分层清晰度。

#### 评论质量

中高。comment writer 成熟，但语义 lifecycle 仍得自己补齐。

#### 扩展性

高。

#### 安全性

中高。

#### 对 self-managed GitLab 适配难度

中高。

### 6.4 三案对比总结

| 维度 | A. 纯 webhook service | B. CI job 驱动 | C. webhook + reviewdog 混合 |
|---|---|---|---|
| 平台化能力 | 最高 | 中 | 高 |
| 多项目扩展 | 最高 | 中 | 高 |
| 去重/状态机 | 最高 | 中 | 高 |
| merge gate 组合 | 高 | 高 | 中高 |
| 评论质量 | 高 | 中高 | 中高 |
| 运维复杂度 | 中高 | 中 | 高 |
| self-managed 适配 | 中 | 低到中 | 中高 |
| 安全边界 | 高 | 中 | 中高 |
| 适合作为主方案 | **是** | 适合作为特殊边界方案 | 适合作为集成方案 |

---

## 7. 推荐主方案

### 7.1 推荐

**推荐主方案：A. 纯 webhook service 架构。**

### 7.2 为什么它优于直接 fork 某个单仓库 reviewer 项目

因为本题核心难点不在“调一次模型 + 发一条评论”，而在于：

- 事件覆盖面与接入治理；
- MR version 感知；
- 结构化 findings 到 GitLab anchor 的精确映射；
- dedupe / rerun / resolve / stale 生命周期；
- 多项目配置继承；
- 安全与审计；
- 与 GitLab merge checks 的组合。

单仓库 reviewer 项目最多帮你省 10%-20% 的“API 调通”工作，却无法承担 80% 的平台工程问题。

### 7.3 核心组件

1. **Ingress API**：接收 system/group/project hooks。
2. **Event normalizer**：识别 MR create/update/new commit，生成标准事件。
3. **Scheduler**：生成 review run，控制并发、抖动、延迟重试。
4. **GitLab adapter**：
   - MR / versions / diffs / discussions / notes / status checks API
   - shallow fetch / MR head ref 可选
5. **Context assembler**：
   - diff hunk
   - 邻近代码
   - `REVIEW.md`
   - 历史 bot discussions 摘要
6. **LLM provider adapter**：OpenAI / Anthropic / Azure / Bedrock / Vertex
7. **Schema validator / parser**
8. **Finding engine**：fingerprint、dedupe、state transition
9. **Comment writer**：diff/file/general 三档回写
10. **Run summary / merge gate adapter**
11. **Policy/config service**
12. **Observability & audit**

### 7.4 部署方式

建议单独部署为内部服务，最小拓扑：

- 2 个 stateless ingress 实例
- 2 个 worker 实例
- PostgreSQL
- Redis（或用 Postgres outbox / queue 简化 MVP）
- 可选 S3 兼容对象存储保存原始 payload / prompt / response（按保留策略）

### 7.5 最小可用版本范围

建议：

- **最低支持：GitLab Self-Managed 16.4+**
  - 原因：Discussions API 已支持 `position_type=file`，便于 file-level fallback。
- **推荐部署：16.9+ / 17.x+**
  - 原因：现代 webhook / API / UI 体验更稳定，减少边缘兼容成本。
- **若要用 external status checks**：
  - Ultimate；
  - self-managed 15.9+ 才是无 feature flag 的稳定状态；
  - 仍不建议把它当深度 review 唯一门禁。

### 7.6 未来演进路线

#### MVP

- System Hook / Group Hook / Project Hook 接入
- 单 provider
- diff discussion 主通道
- root `REVIEW.md`
- fingerprint / dedupe / rerun
- summary note
- metrics + audit

#### Beta

- directory-scoped `REVIEW.md`
- `.gitlab/ai-review.yaml`
- file-level fallback
- multi-provider
- manual commands（rerun / resolve / ignore）
- group-level policy

#### GA

- verification pass
- linked repo / cross-repo context
- policy packs
- optional reviewdog / SARIF adapter
- optional status check adapter
- SSO / UI / 管理面板

---

## 8. 建议的实现路线

### 8.1 MVP 路线

1. **先做中央 service，不做 CI-only。**
2. **先做 API-only 上下文，不执行 repo 代码。**
3. **先做 root 级 `REVIEW.md` + 平台默认规则。**
4. **先只支持 diff discussion + general summary。**
5. **先做单模型单 provider，但 schema 与 provider adapter 抽象要到位。**
6. **先做 dedupe / rerun / resolve 的最小闭环。**

### 8.2 为什么不是先 fork 开源仓库快速改

因为这样通常会把 MVP 错建成：

- 单项目脚本；
- 无 durable state；
- 无 schema-first；
- 无 comment lifecycle；
- 无平台配置；
- 无未来扩展空间。

真正能进入开发阶段的 MVP，应该从一开始就具备正确的系统边界。

---

## 9. 证据附录（官方与一手资料清单）

以下资料用于本次判断；正式开发前建议在 design review 中二次核对：

### GitLab 官方

- System hooks: https://docs.gitlab.com/administration/system_hooks/
- File hooks: https://docs.gitlab.com/administration/file_hooks/
- Group webhooks API: https://docs.gitlab.com/api/group_webhooks/
- Project webhooks API: https://docs.gitlab.com/api/project_webhooks/
- Merge request webhooks: https://docs.gitlab.com/user/project/integrations/webhook_events/
- Merge requests API: https://docs.gitlab.com/api/merge_requests/
- Discussions API: https://docs.gitlab.com/api/discussions/
- Comments and threads: https://docs.gitlab.com/user/discussions/
- Merge requests user docs: https://docs.gitlab.com/user/project/merge_requests/
- External status checks: https://docs.gitlab.com/user/project/merge_requests/status_checks/
- Code Quality: https://docs.gitlab.com/ci/testing/code_quality/
- Code Quality report format: https://docs.gitlab.com/ci/testing/code_quality/#import-code-quality-results-from-a-cicd-job

### 商业化参考

- OpenAI Codex cookbook index: https://developers.openai.com/cookbook/topic/codex/
- Build Code Review with the Codex SDK: https://developers.openai.com/cookbook/examples/codex/build_code_review_with_codex_sdk
- Codex on GitLab secure quality cookbook: https://developers.openai.com/cookbook/examples/codex/secure_quality_gitlab
- Claude Code GitLab CI/CD: https://code.claude.com/docs/en/gitlab-ci-cd
- Claude Code Review: https://code.claude.com/docs/en/code-review
- CodeRabbit self-managed GitLab: https://docs.coderabbit.ai/platforms/self-hosted-gitlab
- CodeRabbit GitLab enterprise/self-hosted deployment: https://docs.coderabbit.ai/self-hosted/gitlab
- CodeRabbit review overview: https://docs.coderabbit.ai/overview/pull-request-review
- CodeRabbit repository settings: https://docs.coderabbit.ai/guides/repository-settings
- CodeRabbit central configuration: https://docs.coderabbit.ai/configuration/central-configuration
- GitLab Duo custom review instructions: https://docs.gitlab.com/user/duo_agent_platform/customize/review_instructions/
- GitLab Duo code review docs: https://docs.gitlab.com/user/gitlab_duo/code_review_classic/
- Gemini Code Assist GitHub review: https://developers.google.com/gemini-code-assist/docs/review-github-code
- Google GitLab GenAI code review codelab: https://codelabs.developers.google.com/genai-for-dev-gitlab-code-review
- Google Developer Connect GitLab: https://docs.cloud.google.com/developer-connect/docs/connect-gitlab
- Devin Review: https://docs.devin.ai/work-with-devin/devin-review
- Devin AGENTS.md: https://docs.devin.ai/onboard-devin/agents-md
- Devin Review autofix / REVIEW.md examples: https://docs.devin.ai/use-cases/gallery/devin-review-autofix

### 开源参考

- dify-gitlab-mr-reviewer: https://github.com/hz-9/dify-gitlab-mr-reviewer
- AICodeReview: https://github.com/murtuzaalisurti/AICodeReview
- ai-mr-review: https://github.com/sercancelenk/ai-mr-review
- redhat-data-and-ai/gitlab-mr-reviewer: https://github.com/redhat-data-and-ai/gitlab-mr-reviewer
- reviewdog: https://github.com/reviewdog/reviewdog
- danger-js: https://github.com/danger/danger-js
- CodeBLEU: https://github.com/k4black/codebleu
