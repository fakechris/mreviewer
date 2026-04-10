# mreviewer

[![Docker Hub](https://img.shields.io/badge/docker-fakechris%2Fmreviewer-blue)](https://hub.docker.com/u/fakechris)

[中文文档](./README.zh-CN.md) | English

面向 GitHub Pull Request 和 GitLab Merge Request 的多模型 AI Code Review 工具。

`mreviewer` 的第一体验目标很简单：装好一个二进制，指向一个真实 PR，然后拿到一份你真能用来改代码的 review。

如果你第一次试这个产品，先走个人 CLI 这条路。别先搭系统，也别先配 Docker。先拿到一份有用的 review，通常五分钟内就够了。

## 第一次结果到底是什么

`mreviewer` 给你的不是一坨模型输出，而是三层东西：

- 一个很短的结论，比如 `approved` 或 `requested_changes`
- 一个 `What To Fix First`，告诉你先修哪几个问题
- 如果你把结果写回 GitHub，它还会在具体代码行下面给出 inline comment

第一次跑出来如果是 `requested_changes`，这不代表配置失败了。它的意思只是：这个 PR 里确实有值得先修的问题。

![mreviewer first review demo](docs/assets/readme/rendered/mreviewer-first-review-demo.gif)

拆开来看是这样的：

| CLI 预演 | GitHub 上的简短结论 |
| --- | --- |
| ![mreviewer CLI dry-run](docs/assets/readme/rendered/mreviewer-cli-real.png) | ![mreviewer GitHub brief](docs/assets/readme/rendered/mreviewer-github-brief-real-3.png) |

| GitHub 上的 inline finding |
| --- |
| ![mreviewer GitHub inline finding](docs/assets/readme/rendered/mreviewer-github-inline-real-2.png) |

你第一次主要看这三件事：

- CLI dry-run：先看 verdict 和最值得修的几条，不写回任何结果。
- GitHub brief：这是 PR 级别的回答，告诉你这次该不该直接合。
- inline comment：这是最有用的部分。它会直接落在改动代码下面，说明具体哪一行有问题、为什么有问题。

## 从这里开始

- **个人 CLI**：一个二进制、本地 SQLite、默认不需要 Docker。最适合第一次体验。
- **企业 Webhook**：适合团队自动 webhook review、队列、历史记录和 `/admin/` 控制面。

## 5 分钟拿到第一份 Review

### 1. 安装 `mreviewer`

推荐方式：

```bash
curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh | bash
```

也支持 Homebrew：

```bash
brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer
brew install mreviewer
```

先确认二进制可用：

```bash
mreviewer version
```

### 2. 生成个人配置模板

```bash
mreviewer init --provider openai
```

这会生成 `config.yaml`，并创建默认本地状态目录 `.mreviewer/state/`。第一次上手不需要手改 YAML。

如果你想直接用智谱 `GLM-5`，可以改成：

```bash
mreviewer init --provider zhipuai
```

对智谱 `GLM-5` / `GLM-5.1`，当前建议优先使用 `tool_call` 路径。`2026-04-10` 的 live probe 表明，这条 endpoint 上 `json_schema strict=true` 仍会出现 `429/code=1305` 拥挤，以及拿到 `200` 但返回非 schema 文本的情况，因此不适合作为首选生产路径（见[验收探针记录](docs/acceptance/2026-04-10-zhipu-glm51-structured-output-probe.md)）。

如果你只想先看生成出来的配置，不写文件：

```bash
mreviewer init --provider openai --dry-run
```

### 3. 先配置最少量凭证

```bash
export OPENAI_API_KEY=...
export GITHUB_TOKEN=...
```

如果使用智谱 `GLM-5`，把 `OPENAI_API_KEY` 换成 `ZHIPUAI_API_KEY`：

```bash
export ZHIPUAI_API_KEY=...
export GITHUB_TOKEN=...
```

第一次体验 GitHub 就够了。之后如果要 review GitLab MR，再补：

```bash
export GITLAB_BASE_URL=https://gitlab.example.com
export GITLAB_TOKEN=...
```

### 4. 先体检，再花 token

```bash
mreviewer doctor --json
```

这一步会提前校验配置、数据库、模型路由和平台凭证。

### 5. 先预演，不写回任何结果

```bash
mreviewer review \
  --target https://github.com/acme/repo/pull/17 \
  --dry-run \
  --output both \
  --reviewer-packs security,architecture,database \
  -vv
```

这是第一次体验最稳的方式。你能先看到 verdict、优先级最高的问题和触发了哪些 reviewer pack，但不会往 GitHub 写任何内容。

### 6. 运行真正的 review

```bash
mreviewer review \
  --target https://github.com/acme/repo/pull/17 \
  --output both \
  --reviewer-packs security,architecture,database
```

默认情况下，这一步会把 review 写回 PR。如果你只想在本地拿结果，不写回平台，再加 `--publish artifact-only`。

真正执行后，你会拿到：

- 一眼能看懂的 verdict
- 一小组最值得先修的问题
- 直接落到改动代码下面的 inline comments
- 适合脚本和 agent 消费的 JSON 输出

### 7. 可选：本地启动 webhook 和 admin UI

```bash
mreviewer serve
```

默认会启动：
- 本地 SQLite：`.mreviewer/state/mreviewer.db`
- GitLab webhook：`POST /webhook`
- GitHub webhook：`POST /github/webhook`
- 本地控制面：`/admin/`

如需自定义数据库位置，可使用 `mreviewer serve --db file:/custom/path.db`。

如果你只是想确认运行计划，不真的启动服务：

```bash
mreviewer serve --dry-run -vv
```

## 个人 CLI 一览

- `mreviewer init`：生成个人配置模板
- `mreviewer doctor`：检查配置、数据库、provider route 和平台凭证
- `mreviewer review`：review 单个 GitHub/GitLab 目标
- `mreviewer serve`：在单进程中启动 ingress + worker，并自动执行 embedded migrations
- `mreviewer help`：查看顶层或子命令帮助
- `mreviewer version`：查看当前安装版本

为了兼容旧用法，`mreviewer --target ...` 仍然等价于 `mreviewer review --target ...`。

## 最常用的第一次体验命令

```bash
# 安装
curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh | bash

# 生成配置
mreviewer init --provider openai

# 或直接用智谱 GLM-5
mreviewer init --provider zhipuai

# 智谱当前建议保持 output_mode=tool_call
# 不建议把 strict json_schema 作为主路径

# 校验环境
mreviewer doctor

# 先 dry-run 预演
mreviewer review --target <pr-or-mr-url> --dry-run -vv

# 再正式执行
mreviewer review --target <pr-or-mr-url> --output both --publish artifact-only
```

## CLI 行为与输出

关键参数：

- `--target`: GitHub PR 或 GitLab MR URL
- `--targets`: 逗号分隔的 GitHub/GitLab PR 或 MR URL，用于多目标 review 和聚合 comparison
- `--output`: `markdown`、`json` 或 `both`
- `--dry-run` / `--dryrun`：只解析和输出，不产生 publish / status 副作用
- `--verbose`、`-v`、`-vv`、`-vvv`、`-vvvv`：逐级提升 trace 细节
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

## 什么情况下切到企业 Webhook

当你需要：
- 自动 GitLab/GitHub webhook review
- MySQL 支撑的控制面和历史记录
- ingress / worker 分离部署
- operator actions、dashboard 和队列可观测性

就应该走企业路径。

## 企业 Webhook 快速启动

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

## 技术文档

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
