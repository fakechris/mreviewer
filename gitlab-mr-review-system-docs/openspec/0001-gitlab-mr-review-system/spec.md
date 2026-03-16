# Spec 0001: GitLab MR Review System

状态：Draft
日期：2026-03-16

## 1. 系统目标

构建一个面向 self-managed GitLab 的自动代码审查系统，在 MR 创建、更新与新增 commit 时自动运行语义级 code review，并将每个 bug 作为独立 review finding 回写为 GitLab discussion/thread，支持 dedupe、rerun、resolve、merge gate 集成，以及多项目/多 group 扩展。

## 2. 术语

- **MR**：Merge Request
- **MR Version**：GitLab 为某个 MR 维护的 diff 版本，携带 `base/start/head` SHA 和 `patch_id_sha`
- **Review Run**：针对一个 MR 某一版 HEAD 的一次审查执行
- **Finding**：模型输出并经系统归一化后的独立问题
- **Anchor**：finding 在 GitLab 变更中的定位，可能为 line / range / file / general
- **Discussion**：GitLab 中的线程对象
- **Dedupe**：识别“这是同一个历史问题”的过程
- **Superseded**：旧 thread 因代码移动/重发被新 thread 取代

## 3. 目标环境与约束

### 3.1 支持环境

- GitLab Self-Managed 16.4+
- PostgreSQL 14+
- Redis 7+（可选，MVP 可用 Postgres outbox）

### 3.2 外部依赖

- GitLab REST API
- 至少一个外部 LLM provider API

### 3.3 重要约束

- 不执行仓库中的任意代码。
- 不信任仓库内容中的指令文本。
- 所有 comment 写回必须幂等且可审计。
- 所有模型输出必须通过 schema 校验后才能进入写回链路。

## 4. 功能需求

### FR-1 事件接入

系统必须支持以下触发来源：

- System Hook
- Group Hook
- Project Hook
- 预留 CI 触发输入

系统至少要识别：

- MR created/opened
- MR updated
- source branch push / new commit on MR
- MR closed / merged（用于终止或清理活跃状态）

### FR-2 事件归一化与去重

系统必须将不同来源 webhook 归一化为统一内部事件格式，并以 `instance + project + mr_iid + head_sha + trigger_type` 级别做幂等去重。

### FR-3 MR 版本解析

系统必须能够：

- 获取最新 MR version
- 获取对应 `base_sha` / `start_sha` / `head_sha`
- 获取 `patch_id_sha`
- 在 diff 尚未就绪时延迟重试

### FR-4 Diff 与上下文组装

系统必须从 GitLab 获取：

- 变更文件列表
- diff hunks
- 文件大小、generated / collapsed / too_large 等元信息
- 根目录 `REVIEW.md`（MVP）
- 历史 bot discussions 摘要（MVP 可只取 active findings）

系统应支持 path include/exclude 过滤。

### FR-5 模型调用

系统必须通过 provider adapter 调用外部 LLM，并以严格 JSON schema 约束返回格式。

### FR-6 Finding 归一化

系统必须把模型结果归一化为内部 finding，并包含：

- category
- severity
- confidence
- path
- anchor
- explanation
- evidence
- optional suggested patch
- canonical fingerprint ingredients

### FR-7 Comment 写回

系统必须优先将 finding 写为 GitLab diff discussion。

当且仅当无法稳定行定位时：

- 退化为 file-level discussion；
- 再退化为 general note。

### FR-8 Dedupe 与生命周期

系统必须支持：

- 同一 finding 在 rerun / 新 commit 后不重复刷屏
- 相同问题行号变化时的重新绑定或 supersede
- issue 消失后的 stale / fixed 状态转换
- bot discussion 的 auto-resolve（按配置）

### FR-9 Rerun

系统必须支持：

- 同 HEAD 的手动 rerun
- 新 HEAD 的自动 rerun
- 配置变更后的 rerun

### FR-10 配置与规则

系统必须支持配置层级：

- 平台默认
- project 配置（MVP）
- group 配置（Beta）
- repo 内 `REVIEW.md`
- repo 内 `.gitlab/ai-review.yaml`（Beta）

### FR-11 可观测性与审计

系统必须记录：

- webhook 收到与验证结果
- review run 生命周期
- provider 调用时延、token、错误码
- parser / anchor / writer 失败原因
- 创建、更新、resolve discussion 的审计日志

### FR-12 Merge gate 集成

系统必须至少支持：

- unresolved discussions 作为 merge gate 的集成建议与配置说明

系统应支持：

- external status checks adapter（Ultimate / 可选）
- CI status adapter（CI 架构 / 可选）

## 5. 非功能需求

### NFR-1 幂等

重复 webhook 不得产生重复 run 或重复 comment。

### NFR-2 可恢复

任何临时失败（GitLab API / provider / 解析 / 网络）都必须可重试，并保留失败上下文。

### NFR-3 安全

- webhook 必须验签/验 token
- 凭据最小权限
- repo 内容视为不可信
- 默认最小代码外发

### NFR-4 性能

- 小中型 MR 的 P90 完成时间应在目标阈值内（由环境与模型决定，默认目标 10 分钟）
- 在同一 project 下并发 MR 时不得互相污染状态

### NFR-5 可扩展

架构必须允许未来增加：

- 多 provider
- 多种 output adapters
- verification pass
- cross-repo context

## 6. 不做事项

- 自动 approve MR
- 自动 merge MR
- 在 MVP 中自动提交修复 commit
- 在 MVP 中支持任意 agent 工具执行

## 7. 验收标准

### AC-1 基础触发

给定一个支持的 GitLab self-managed 实例，配置好 webhook 后：

- 创建 MR 时系统收到事件并创建 review run
- MR push 新 commit 时系统为新 HEAD 创建新 run
- 重放相同 webhook 不产生重复 run

### AC-2 写回

给定一个包含可定位问题的 MR：

- 系统可以在正确文件/行写出 diff discussion
- discussion 可在 GitLab 中被 resolve

### AC-3 去重

给定一个 finding 已在 run-1 中发出：

- 若 run-2 在同一问题上再次命中，不得重复发新 discussion
- 若问题修复后 run-3 不再命中，该 finding 状态应转换为 fixed 或 stale

### AC-4 兜底

当某 finding 无法定位到当前 diff 行时：

- 系统应退化到 file-level discussion 或 general note
- 不得因为单个 finding 的定位失败中断整个 run

### AC-5 失败恢复

当 provider 超时、GitLab API 429/5xx 或 parser 失败时：

- run 状态应可追踪
- 系统可安全重试
- 不得产生重复 comments
