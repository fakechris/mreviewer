# mreviewer

[![Docker Hub](https://img.shields.io/badge/docker-fakechris%2Fmreviewer-blue)](https://hub.docker.com/u/fakechris)

[中文文档](./README.zh-CN.md) | English

GitLab Merge Request AI 代码审查工具。支持自托管、多模型、SQLite/MySQL。

## 特性

- 🤖 **多模型支持**: MiniMax、OpenAI、Anthropic、DeepSeek 等
- 🗄️ **灵活存储**: SQLite（单机）或 MySQL（生产）
- 🌐 **GitLab 原生**: Webhook 集成、讨论评论、CI 门禁
- 🧰 **Portable Review Council CLI**: 通过统一 CLI 直接审查 GitHub / GitLab PR
- 🔄 **智能去重**: 指纹匹配 + LLM 语义去重
- 📊 **可观测性**: Grafana 仪表板、审计日志、指标

## 部署决策

- 个人 / 小团队试用：直接走单 provider quick start、Webhook 和内置 `/admin/` 控制面
- 企业默认：使用 MySQL 作为中心存储，可选 Redis 协调，配合 Webhook 自动触发和 `/admin/` 页面查看排队、并发、失败
- SQLite 继续保留给单机场景，但共享环境和生产环境默认推荐 MySQL

## 快速开始

### 前置要求

- Docker & Docker Compose
- GitLab 实例及 API 访问权限
- LLM 提供商 API Key（MiniMax、OpenAI 等）

### 方式 1：极简部署（无需 Git）

**适合新手** - 下载 2 个文件即可运行

1. **下载文件**：
   - [docker-compose.prod.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml)
   - [.env 模板](https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example)（重命名为 `.env`）

2. **编辑 `.env`，从下面三条等价 quick start 里选一条**：

#### 方式 1A：MiniMax
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=minimax
LLM_API_KEY=your_minimax_key
LLM_BASE_URL=https://api.minimaxi.com/anthropic
LLM_MODEL=MiniMax-M2.7-highspeed
```

#### 方式 1B：Anthropic
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=anthropic
LLM_API_KEY=your_anthropic_key
LLM_BASE_URL=https://api.anthropic.com
LLM_MODEL=claude-sonnet-4-6
```

#### 方式 1C：ChatGPT / OpenAI
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=openai
LLM_API_KEY=your_openai_key
LLM_BASE_URL=https://api.openai.com/v1
LLM_MODEL=gpt-5.4
```

这三条 quick start 共用同一套 env-only 合约和同一条启动命令。

3. **启动服务**：
```bash
docker compose -f docker-compose.prod.yaml up -d
```

4. **验证**：
```bash
docker compose -f docker-compose.prod.yaml logs -f worker
```

### 方式 2：高级无 Git 部署（多提供商 / OpenAI / 自定义路由）

**适合运维/高级用户** - 下载 4 个文件并挂载自定义配置

1. **下载文件**：
   - [docker-compose.prod.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml)
   - [docker-compose.prod.config.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.config.yaml)
   - [.env 模板](https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example)（重命名为 `.env`）
   - [config 示例](https://raw.githubusercontent.com/fakechris/mreviewer/main/config.example.yaml)（重命名为 `config.yaml`）

2. **编辑 `.env` 和 `config.yaml`**：
- `docker-compose.prod.yaml` 会把整个 `.env` 透传进容器，所以自定义 provider 密钥也会生效。
- `docker-compose.prod.config.yaml` 会把本地 `config.yaml` 挂载到 `/app/config.yaml`。
- `config.example.yaml` 支持 `${VAR}` 语法，启动时会自动展开环境变量。

3. **启动服务**：
```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml up -d
```

DeepSeek、Fireworks、混合路由或 SQLite 部署请走这条路径。可直接参考 [config.example.yaml](./config.example.yaml)。

4. **验证**：
```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml logs -f worker
```

### 方式 3：完整克隆（开发者）

**适合开发者** - 克隆仓库并运行本地源码构建

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
# 编辑 .env 填入凭证
docker compose up -d --build
```

这条路径会从你当前 checkout 本地构建 `ingress` 和 `worker`，所以修改代码后重新构建即可验证。

### Portable Review Council CLI

新的 `mreviewer` CLI 可以直接对 GitHub 或 GitLab 的 PR / MR URL 运行 portable review council。

本地源码运行方式：

```bash
go run ./cmd/mreviewer \
  --target https://github.com/acme/repo/pull/17 \
  --output both \
  --publish full-review-comments \
  --reviewer-packs security,architecture,database
```

容器内运行方式：

```bash
docker compose exec worker /app/mreviewer \
  --target https://github.com/acme/repo/pull/17 \
  --output json \
  --publish artifact-only
```

关键参数：

- `--target`: GitHub PR 或 GitLab MR URL
- `--targets`: 逗号分隔的 GitHub/GitLab PR 或 MR URL，用于多目标 review 和聚合 comparison
- `--output`: `markdown`、`json` 或 `both`
- `--publish`: `full-review-comments`、`summary-only` 或 `artifact-only`
- `--reviewer-packs`: 逗号分隔的 reviewer pack 列表
- `--route`: provider route override
- `--advisor-route`: 可选的更强的 second opinion provider route
- `--exit-mode`: `never` 或 `requested_changes`；当最终 verdict 为需要修改时返回退出码 `3`
- `--compare-live`: 逗号分隔的目标 PR/MR 上已有 reviewer 标识，例如 `reviewer-a,reviewer-b`
- `--compare-artifacts`: 逗号分隔的外部 JSON artifact 路径

当提供 compare 参数时，CLI 会在 JSON 输出里附带 comparison report，包括 agreement rate、shared findings 和各 reviewer 的 unique findings。
当提供 `--targets` 时，JSON 输出还会包含 `aggregate_comparison`，用于在一次运行里比较多个 GitHub/GitLab 变更上的 reviewer 一致性。
当启用 advisor 或 benchmark 时，JSON 输出还会包含 `advisor_artifact`、`judge_verdict` 和 `decision_benchmark`。
当目标是 GitHub PR 时，CLI 还会在 review 运行中和结束后更新 `mreviewer/ai-review` 这个 commit status。

运行时环境变量覆盖：

- `REVIEW_PACKS`: worker/runtime 默认启用的 reviewer packs
- `REVIEW_ADVISOR_ROUTE`: CLI/runtime 默认使用的更强的 second opinion route
- `REVIEW_COMPARE_REVIEWERS`: runtime 自动 comparison 时要拉取的外部 reviewer 列表

### 配置 GitLab Webhook

**自动审查必需**

1. 进入 GitLab 项目 → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
   - 如果是在局域网内联调，把 `your-server` 换成你机器的局域网 IP，例如 `http://10.0.0.16:3100/webhook`
   - 只有 GitLab 和 mreviewer 跑在同一台机器上时，才应该使用 `localhost`
3. Secret: 填入 `.env` 中的 `GITLAB_WEBHOOK_SECRET`
4. 触发器: 勾选 "Merge request events"
5. 点击 "Add webhook"

如果 GitLab 返回 `Invalid url given`，需要让 GitLab 管理员打开 `Allow requests to the local network from web hooks and services`，或者改用公网 HTTPS tunnel，而不是直接填局域网 IP。

📖 详细配置: [WEBHOOK.md](./WEBHOOK.md)

### Admin 控制面

`ingress` 现在会暴露只读运维页面 `/admin/`，以及以下 JSON 接口：

- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`

配置 `MREVIEWER_ADMIN_TOKEN` 或 `ADMIN_TOKEN` 后，这些路由会要求 `Authorization: Bearer <token>`。这让运维可以直接查看排队、活跃 worker 和失败情况，而不必先翻日志。

### 手动触发（可选）

无需 webhook 手动触发审查：

```bash
docker compose exec worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait
```

**注意**: 需要在 `.env` 中配置 `GITLAB_BASE_URL`、`GITLAB_TOKEN` 和 LLM API 密钥

## 架构

```
GitLab → ingress (webhook) → MySQL/SQLite
                           ↓
                        worker → LLM Provider
                           ↓
                    GitLab (discussions)
```

## 文档

- [GitLab Webhook 配置](./WEBHOOK.md) - 三种配置方式（项目/组/系统）
- [Docker 部署](./DEPLOYMENT.md) - 构建和生产部署
- [企业 Webhook 架构](./docs/architecture/enterprise-webhook.md) - 队列语义、并发模型与控制面设计
- [Admin 控制台说明](./docs/operations/admin-dashboard.md) - `/admin/` 使用方式与 Bearer 鉴权
- [故障处理手册](./docs/operations/failure-playbook.md) - provider、worker、superseded run 的排障方式
- [贡献指南](./CONTRIBUTING.md) - 如何贡献
- [配置参考](./config.yaml) - 安全默认运行配置
- [高级配置模板](./config.example.yaml) - 带环境变量展开的多 provider 示例

## Roadmap

查看 [TODOS.md](./TODOS.md) 了解当前优先级。

## 许可证

MIT License - 详见 [LICENSE](./LICENSE)
