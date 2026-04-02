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

## 选择你的模式

- **个人 CLI**：单可执行文件、本地 SQLite、默认不需要 Docker，适合个人开发者按需 review GitHub/GitLab PR。
- **企业 Webhook**：`ingress` + `worker` + `/admin/` 控制面，MySQL 优先，可选 Redis，适合自动化 webhook review 和运维排障。

## 个人 CLI 快速开始

### 1. 安装二进制

直接从 GitHub Releases 下载，或者运行安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh | bash
```

也支持直接通过仓库 tap 使用 Homebrew 安装：

```bash
brew tap fakechris/mreviewer
brew install mreviewer
```

### 2. 生成个人配置

```bash
mreviewer init --provider openai
```

这会生成 `config.yaml`，并创建默认本地状态目录 `.mreviewer/state/`。

### 3. 导出凭证

```bash
export OPENAI_API_KEY=...
export GITHUB_TOKEN=...
# 可选：
export GITLAB_BASE_URL=https://gitlab.example.com
export GITLAB_TOKEN=...
```

### 4. 检查配置

```bash
mreviewer doctor
```

### 5. 运行 review

```bash
mreviewer review \
  --target https://github.com/acme/repo/pull/17 \
  --output both \
  --publish artifact-only \
  --reviewer-packs security,architecture,database
```

### 6. 可选：本地启动 webhook 和 dashboard

```bash
mreviewer serve
```

默认会启动：
- 本地 SQLite：`.mreviewer/state/mreviewer.db`
- GitLab webhook：`POST /webhook`
- GitHub webhook：`POST /github/webhook`
- 本地控制面：`/admin/`

如需自定义数据库位置，可使用 `mreviewer serve --db file:/custom/path.db`。

## 个人 CLI 命令

- `mreviewer init`：生成个人配置模板
- `mreviewer doctor`：检查配置、数据库、provider route 和平台凭证
- `mreviewer review`：review 单个 GitHub/GitLab 目标
- `mreviewer serve`：在单进程中启动 ingress + worker，并自动执行 embedded migrations

为了兼容旧用法，`mreviewer --target ...` 仍然等价于 `mreviewer review --target ...`。

## CLI 输出能力

关键参数：

- `--target`: GitHub PR 或 GitLab MR URL
- `--targets`: 逗号分隔的 GitHub/GitLab PR 或 MR URL，用于多目标 review 和聚合 comparison
- `--output`: `markdown`、`json` 或 `both`
- `--publish`: `full-review-comments`、`summary-only` 或 `artifact-only`
- `--reviewer-packs`: 逗号分隔的 reviewer pack 列表
- `--route`: model 或 model chain override
- `--advisor-route`: 可选的更强 second opinion model / chain override
- `--exit-mode`: `never` 或 `requested_changes`；最终 verdict 需要修改时返回退出码 `3`
- `--compare-live`: 逗号分隔的已有 reviewer 标识
- `--compare-artifacts`: 逗号分隔的外部 JSON artifact 路径

JSON 输出包含：
- `review_brief`
- `judge_verdict`
- `decision_benchmark`
- `comparison`
- `aggregate_comparison`
- `advisor_artifact`

Markdown 输出会先给出 `# Review Decision Brief`，并组织为：
- `Final Verdict`
- `What To Fix First`
- `Specialist Signals`
- 启用 comparison 时的 `Reviewer Overlap`

运行时环境变量覆盖：

- `REVIEW_PACKS`
- `REVIEW_MODEL_CHAIN`
- `REVIEW_ADVISOR_CHAIN`
- `REVIEW_COMPARE_REVIEWERS`

## 企业 Webhook 部署

当你需要：
- 自动 GitLab/GitHub webhook review
- MySQL 支撑的控制面和历史记录
- ingress / worker 分离部署
- operator actions、dashboard 和队列可观测性

就应该走企业路径。

### 企业快速启动

```bash
cp .env.example .env
docker compose up -d --build
```

镜像部署：

```bash
docker compose -f docker-compose.prod.yaml up -d
```

挂载自定义配置：

```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml up -d
```

`docker-compose.yaml` 适合开发者本地源码构建，`docker-compose.prod.yaml` 适合企业式部署。企业默认推荐 MySQL；SQLite 仍适合个人或单机环境。

### 配置 GitLab Webhook

1. GitLab 项目 → Settings → Webhooks
2. URL：`http://your-server:3100/webhook`
   - 局域网联调时，把 `your-server` 换成局域网 IP，例如 `http://10.0.0.16:3100/webhook`
   - 除非 GitLab 和 mreviewer 在同一台机器，否则不要用 `localhost`
3. Secret：`GITLAB_WEBHOOK_SECRET`
4. Trigger：勾选 `Merge request events`
5. 点击 `Add webhook`

如果 GitLab 返回 `Invalid url given`，需要 GitLab 管理员启用 `Allow requests to the local network from web hooks and services`，或者改用公网 HTTPS tunnel。

GitHub webhook 路径为 `POST /github/webhook`，同样走 ingress / worker 控制面。

📖 详细配置见 [WEBHOOK.md](./WEBHOOK.md)

## Admin 控制面

企业部署和本地 `serve` 都会暴露 `/admin/`。

关键接口：

- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`
- `/admin/api/runs`
- `/admin/api/trends`
- `/admin/api/ownership`
- `/admin/api/identities`

配置 `MREVIEWER_ADMIN_TOKEN` 或 `ADMIN_TOKEN` 后，这些路由要求 `Authorization: Bearer <token>`。

## 手动触发（企业可选）

无需等待 webhook 可直接手动触发 GitLab review：

```bash
docker compose exec worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait
```

worker 镜像里也内置了 `/app/mreviewer`，可用于直接运行 CLI review。

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
