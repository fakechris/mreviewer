# PRD：自托管 GitLab MR 自动代码审查系统

版本：v0.1
日期：2026-03-16

## 1. 产品背景

在 self-managed GitLab 环境中，传统静态分析与人工 review 存在三类缺口：

1. **语义问题覆盖不足**：lint / SAST 很难识别跨文件、跨逻辑分支的业务 bug、边界条件、错误处理遗漏。
2. **审查吞吐不足**：AI 生成代码增多后，人工 reviewer 的注意力更像稀缺资源。
3. **平台治理缺位**：很多“AI review bot”只能发 summary，缺少可定位、可去重、可 resolve、可治理的工程能力。

因此需要一个平台级系统：在 self-managed GitLab 上自动监听 MR 事件，对变更做 LLM 驱动的语义审查，并把每个 bug 作为独立 discussion 回写到 MR 中，支持 rerun、dedupe、resolve、merge gate 与多项目扩展。

## 2. 产品目标

### 2.1 业务目标

- 提升 MR 中高价值缺陷的自动发现率。
- 降低 reviewer 首轮发现低级/中级 bug 的时间成本。
- 在不显著增加开发者噪声的前提下，把 AI review 融入现有 GitLab 流程。

### 2.2 产品目标

- 在 MR 创建、更新、push 新 commit 时自动触发 review。
- 每个 bug 逐条写回 MR，首选 inline diff discussion。
- 不因 rerun 或新 commit 重复刷屏。
- 支持 bot 自己 resolve 已修复或已过期问题。
- 支持 group/project 多租户扩展。
- 支持外部 LLM provider 切换与审计。

## 3. 目标用户

### 3.1 平台团队 / DevEx 团队

需要统一接入、统一治理、统一观测多项目代码审查能力。

### 3.2 仓库 Maintainer / Tech Lead

希望为自己项目配置 review 规则、路径过滤、敏感策略、门禁策略。

### 3.3 开发者 / Reviewer

希望在 MR 的 Changes 视图里直接看到具体问题、证据与建议，而不是阅读一大段泛泛总结。

### 3.4 安全 / 合规团队

关心代码外发边界、数据保留、provider 选择、token 权限和审计链。

## 4. 用户故事

### 4.1 自动 review

- 作为开发者，我创建一个 MR 后，希望系统自动在几分钟内给出高价值 review 结果。
- 作为开发者，我 push 新 commit 后，希望系统只 review 新变化，不重复之前的问题。

### 4.2 可定位、可交互

- 作为 reviewer，我希望每个问题都挂在对应文件/行上。
- 作为 reviewer，我希望能在 thread 里追问、补充上下文。
- 作为开发者，我修复问题后，希望旧问题被自动 resolve 或标记过期，而不是永久悬挂。

### 4.3 可配置

- 作为 maintainer，我希望在仓库里用 `REVIEW.md` 和配置文件定义 review 规则。
- 作为平台团队，我希望在 group/project 维度下发默认策略并允许 repo override。

### 4.4 可治理

- 作为安全团队，我希望敏感仓库可以禁用代码外发或切换到指定 provider。
- 作为平台团队，我希望审查失败、超时、重复评论率、误报率都有可观测数据。

## 5. 范围定义

### 5.1 MVP In Scope

- System Hook / Group Hook / Project Hook 事件接入。
- 监听 MR 创建、更新、push 新 commit。
- 获取最新 MR version、diff 与最小上下文。
- 支持单 provider 的 LLM 分析。
- 使用严格 JSON schema 作为中间结果。
- diff discussion 主通道、general summary 兜底。
- fingerprint / dedupe / rerun / auto-resolve 最小闭环。
- 根目录 `REVIEW.md`。
- 平台默认配置 + project 配置。
- 基础指标、日志、审计。

### 5.2 Beta Scope

- directory-scoped `REVIEW.md`
- `.gitlab/ai-review.yaml`
- 多 provider
- manual commands（rerun / resolve / ignore）
- file-level discussion fallback
- group 级策略

### 5.3 GA Scope

- verification pass
- linked repositories / 跨仓上下文
- reviewdog / SARIF adapter
- status check adapter
- 管理后台 / 配置 UI
- 组织级策略包与反馈学习

## 6. 非目标（Non-goals）

MVP 不做：

- 自动 approve / 自动 merge
- 自动提交修复 patch
- 通用代码搜索平台 / 长期知识库
- 全量替代静态分析、SAST、Code Owners、Approvals
- 自带 IDE 插件
- 完整 SaaS 门户

## 7. 核心产品原则

1. **少而准**：宁可少发，也不要大量噪声。
2. **定位优先**：有锚点的问题优先于泛泛总结。
3. **生命周期完整**：问题能出现，也能消失，能重跑，能去重。
4. **平台治理优先**：必须支持跨项目统一管理。
5. **安全默认收敛**：默认最小上下文、最小权限、最小外发。

## 8. 成功指标

### 8.1 产品效果指标

- 高价值 finding 命中率（人工标注后 true positive rate）
- 漏报率（离线回放集）
- 重复评论率
- 开发者接受率（被保留/回复/修复的 comment 占比）
- auto-resolved 比例

### 8.2 系统指标

- P50 / P90 首次 review 完成时延
- rerun 完成时延
- comment writer 成功率
- parser failure rate
- webhook 去重命中率
- 平均 token / run 与成本 / run

### 8.3 业务采用指标

- 已接入项目数 / group 数
- 每周被审查 MR 数
- 每周有效 finding 数
- 平台 opt-out 比例

## 9. 运营与权限假设

- 有 GitLab self-managed 实例；优先争取 admin 以接入 System Hook。
- 允许建立 dedicated bot user。
- 可为 bot 分配项目 Developer 权限，极少数需要 Maintainer 的能力不进入 MVP。
- 平台团队拥有数据库、对象存储、日志、指标系统等基本基础设施。
- 外部 LLM 使用需经安全审查，并允许按项目粒度配置 provider。

## 10. 关键约束

- GitLab API 对 discussion anchor 有严格要求，必须以 MR version 的 base/start/head SHA 为准。
- MR 创建后 diff 可能尚未就绪，需要异步重试。
- 外部 status checks pending 超时短，不适合作为长耗时深度 review 的唯一门禁。
- 开发者对重复评论容忍度很低，dedupe 是一等公民。

## 11. 发布策略

### Phase 1：封闭试点

- 3~5 个中等规模项目
- 单 provider
- 默认只发 high / medium confidence bug

### Phase 2：group 级扩展

- 引入 project config
- 允许 path filtering
- 引入 rerun / ignore 命令

### Phase 3：实例级推广

- System Hook 覆盖
- 组织级策略
- 指标面板与稳定性 SLO

## 12. 退出条件

MVP 认为达到可用需满足：

- 能稳定监听 MR create/update/push
- 能在 95% 以上 case 成功回写 discussion 或 fallback note
- 重复评论率 < 5%
- parser failure rate < 1%
- P90 review latency 在设定阈值内（例如小中型 MR 10 分钟内）
- 试点项目 maintainer 愿意继续启用
