# Tasks 0001: GitLab MR Review System

状态：Draft
日期：2026-03-16

## MVP

### 1. 基础工程

- [ ] 初始化服务仓库与目录结构
- [ ] 接入配置系统（env + yaml）
- [ ] 建立 PostgreSQL migration 体系
- [ ] 建立日志、metrics、trace 基础设施

### 2. GitLab 接入

- [ ] 实现 webhook ingress
- [ ] 支持 `X-Gitlab-Token` 验证
- [ ] 归一化 System Hook / Group Hook / Project Hook payload
- [ ] 实现 `hook_events` 去重
- [ ] 实现 GitLab MR / versions / diffs API client

### 3. review run 管线

- [ ] 建立 `review_runs` 数据模型
- [ ] 实现调度器与重试状态机
- [ ] 处理 MR diff not ready 场景
- [ ] 实现 changed files 过滤（generated / binary / lock / vendor）

### 4. 规则加载

- [ ] 读取 project policy
- [ ] 读取仓库根目录 `REVIEW.md`
- [ ] 生成 effective rules digest

### 5. LLM 集成

- [ ] 定义 `ReviewRequest` 与 `ReviewResult` schema
- [ ] 接入一个 provider（建议 OpenAI 或 Anthropic）
- [ ] 实现 schema validator
- [ ] 实现 parser fallback

### 6. finding engine

- [ ] 设计 anchor / semantic fingerprint 算法
- [ ] 建立 `review_findings` 表
- [ ] 实现 active / fixed / stale / superseded 状态转换
- [ ] 实现同 HEAD 与新 HEAD 去重

### 7. GitLab 回写

- [ ] 实现 diff discussion writer
- [ ] 实现 general summary note writer
- [ ] 实现 comment action 幂等
- [ ] 实现 bot discussion auto-resolve

### 8. 试点能力

- [ ] 建立 3 个试点项目的 project policy
- [ ] 建立基础 dashboard
- [ ] 建立 replay 工具（用历史 webhook / MR 回放）

## Beta

### 1. 规则与配置增强

- [ ] 支持 `.gitlab/ai-review.yaml`
- [ ] 支持 directory-scoped `REVIEW.md`
- [ ] 支持 group policy
- [ ] 支持 provider route per project/group

### 2. 写回增强

- [ ] 支持 file-level discussion fallback
- [ ] 支持 multi-line range comments
- [ ] 支持 optional GitLab suggestions

### 3. 控制面

- [ ] 支持 MR comment command：`/ai-review rerun`
- [ ] 支持 `ignore` / `resolve` / `focus path` 命令
- [ ] 支持 note/comment event 触发控制面

### 4. 稳定性

- [ ] 增加 provider fallback
- [ ] 增加 rate-limit 策略
- [ ] 增加大 MR 降级策略
- [ ] 增加 parser / anchor failure 分析报表

### 5. 评估体系

- [ ] 建立人工 adjudication 流程
- [ ] 建立误报 / 漏报离线回放集
- [ ] 建立重复评论率监控

## GA

### 1. 审查质量提升

- [ ] 引入 verification pass
- [ ] 引入规则包（security / migration / API / concurrency）
- [ ] 引入 linked repositories / cross-repo context

### 2. 输出适配器

- [ ] 实现 RDJSON adapter
- [ ] 实现 SARIF adapter
- [ ] 实现 optional reviewdog publisher
- [ ] 实现 optional external status check adapter

### 3. 管理与治理

- [ ] 配置 UI / 管理后台
- [ ] SSO / RBAC
- [ ] 审计查询界面
- [ ] 成本与 provider 使用报表

### 4. 平台化

- [ ] 支持多 GitLab 实例
- [ ] 支持多 region / 多 provider route
- [ ] 引入配额、限速、租户隔离

## 里程碑建议

### Milestone 1：PoC（2~3 周）

- webhook 收到
- diff 拉取成功
- 单 provider 输出 1 条有效 inline discussion

### Milestone 2：MVP Alpha（4~6 周）

- dedupe / rerun / auto-resolve 闭环打通
- 3 个试点项目可运行

### Milestone 3：MVP GA（8~10 周）

- observability / audit / 失败恢复到位
- 明确 rollout playbook 与 SLO
