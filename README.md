# mreviewer

GitLab Merge Request 自动 Code Review 服务，面向单机 Docker Compose 部署。

## 组件

- `ingress`: 接收 GitLab webhook，请求入口，默认端口 `3100`
- `worker`: 拉取 MR 上下文、调用模型、落库 review 结果
- `mysql`: MySQL 8.4，业务主库
- `redis`: Redis 7，限流/协调降级依赖

## 生产拓扑

单机部署：

- GitLab -> `ingress` `/webhook`
- `ingress` -> MySQL
- `worker` -> MySQL / Redis / GitLab API / LLM Provider
- `worker` -> 持久化 findings / summary / gate 结果，并回写 GitLab discussion / note

## 环境变量

复制配置模板：

```bash
cp .env.example .env
```

如果你已经在当前 shell 里导出了 `GITLAB_TOKEN` 和 `MINIMAX_API_KEY`，也可以直接一键生成：

```bash
scripts/init-local-env.sh
```

关键变量：

- `APP_ENV`: 建议生产设为 `production`
- `PORT`: ingress 监听端口，默认 `3100`
- `GITLAB_BASE_URL`: 你的 GitLab 地址，例如 `https://gitlab.example.com`
- `GITLAB_TOKEN`: GitLab API Token
- `GITLAB_WEBHOOK_SECRET`: GitLab webhook secret
- `ANTHROPIC_BASE_URL`: 模型供应商 Anthropic-compatible 地址
- `ANTHROPIC_API_KEY`: 模型 API Key
- `ANTHROPIC_MODEL`: 模型名

如果你使用默认 Docker Compose 部署：

- 一般只需要填写 `GITLAB_*` 和模型供应商变量
- `MYSQL_DSN` 和 `REDIS_ADDR` 通常不需要改
- compose 会自动启动并初始化 MySQL / Redis

`MYSQL_DSN` 和 `REDIS_ADDR` 主要给这两类场景使用：

- 你在宿主机直接执行 `go run ./cmd/manual-trigger ...`
- 你不用仓库自带的 compose，而是改接外部 MySQL / Redis

## Review 输出语言

默认输出语言是简体中文 `zh-CN`。

当前不是通过环境变量配置，而是通过项目策略配置：

1. 仓库内 `.gitlab/ai-review.yaml`
2. 或 `project_policies.extra` 的 JSON

推荐直接在仓库里放 `.gitlab/ai-review.yaml`：

```yaml
output_language: zh-CN
```

如果想切成英文：

```yaml
output_language: en-US
```

完整示例：

```yaml
confidence_threshold: 0.85
severity_threshold: high
provider_route: default
output_language: zh-CN
max_files: 50
max_changed_lines: 1500
context_lines_before: 25
context_lines_after: 15
```

## GitLab 配置

1. 在 GitLab 创建可读取 MR / diff / note，并可写评论的 token
2. 在目标项目中配置 webhook：
   - URL: `http(s)://<your-host>:3100/webhook`
   - Secret: 与 `GITLAB_WEBHOOK_SECRET` 一致
   - 事件：Merge Request events、Note events
3. 确保 GitLab 可以访问部署主机的 `3100` 端口

## 模型供应商配置

当前实现使用 Anthropic-compatible 接口。

代码里生产 `worker` 目前实例化的是 `llm.NewMiniMaxProvider(...)`，也就是通过 Anthropic-compatible 协议接 MiniMax。

最少需要：

- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL`

如果你更习惯直接放 MiniMax 环境变量，也支持以下回退：

- `MINIMAX_API_KEY`
- `MINIMAX_BASE_URL`
- `MINIMAX_MODEL`

当 `ANTHROPIC_*` 没有配置时，worker 会自动回退到 `MINIMAX_*`。

MiniMax 当前的稳定性和已知问题，单独整理在：

- [docs/minimax-known-issues.md](/Users/chris/workspace/mreviewer/docs/minimax-known-issues.md)

## 安装与初始化

### 1. 准备配置

```bash
cp .env.example .env
```

或者直接使用初始化脚本自动生成：

```bash
scripts/init-local-env.sh
```

如果你使用默认 Docker Compose，最短配置就是修改 `.env` 里的这些值：

```env
GITLAB_BASE_URL=https://github.91jinrong.com
GITLAB_TOKEN=你的_gitlab_token
GITLAB_WEBHOOK_SECRET=自己生成的一串随机串

MINIMAX_API_KEY=你的_minimax_token
MINIMAX_BASE_URL=https://api.minimaxi.com/anthropic
MINIMAX_MODEL=MiniMax-M2.7-highspeed
```

其余数据库和 Redis 配置默认就能工作，不需要你手动创建库、建表、配账号。

如果你已经把 `GITLAB_TOKEN` 和 `MINIMAX_API_KEY` 导出在当前 shell，推荐直接执行：

```bash
scripts/init-local-env.sh --force
```

脚本会：

- 生成默认 Compose 可用的 `.env`
- 自动填入 `GITLAB_TOKEN` 和 `MINIMAX_API_KEY`
- 默认把 `GITLAB_BASE_URL` 设为 `https://github.91jinrong.com`
- 自动生成一个随机 `GITLAB_WEBHOOK_SECRET`
- 如果 `.env` 已存在，会先备份成 `.env.bak.<timestamp>`

### 2. 启动依赖与服务

```bash
docker compose up -d --build
```

这会启动：

- `mysql`
- `redis`
- `migrate`（一次性执行 Goose 数据库迁移）
- `ingress`
- `worker`

`ingress` 和 `worker` 会在 `migrate` 成功后再启动，确保 MySQL schema 已完成初始化。

默认 Compose 会自动初始化这套数据库：

- MySQL host: `127.0.0.1:3306`
- database: `mreviewer`
- user: `mreviewer`
- password: `mreviewer_password`

其中：

- `mysql` 容器负责创建数据库和账号
- `migrate` 容器负责执行 `migrations/` 下的 Goose 迁移，自动创建表结构

所以默认部署下，你不需要手工执行任何建库建表 SQL。

### 3. 健康检查

```bash
curl -i http://127.0.0.1:3100/health
docker compose ps
```

### 4. 查看数据库是否初始化成功

```bash
docker compose logs migrate
docker exec -it mreviewer-mysql mysql -umreviewer -pmreviewer_password mreviewer -e "show tables;"
```

如果 `migrate` 成功，你会看到 `gitlab_instances`、`projects`、`merge_requests`、`review_runs`、`review_findings` 等表。

## 本地 End-to-End 跑通步骤

1. 启动整套服务：`docker compose up -d --build`
   - 首次启动会先运行一次 `migrate` 容器应用 `migrations/` 下的 Goose 迁移
2. 确认 `ingress` 健康：`curl http://127.0.0.1:3100/health`
3. 在 GitLab 项目中配置 webhook 到 `/webhook`
4. 创建或更新一个 Merge Request
5. 观察 `ingress` 日志收到 webhook：

```bash
docker compose logs -f ingress
```

6. 观察 `worker` 处理、拉取上下文、调用模型、落库 findings / summary：

```bash
docker compose logs -f worker
```

7. 当前主分支可确认：
   - `review_runs` / `review_findings` 已落库
   - worker 会把 finding 写回 GitLab discussion / summary note
   - note command（如 rerun / ignore / resolve / focus）链路可继续验证
   - 大 MR 降级 summary / provider route / beta 行为正常

## 手动触发单个 MR

如果你暂时不想接 webhook，可以先手动把指定 MR 入队，让现有 `worker` 按正常链路处理。

先确保：

- `mysql` / `redis` / `migrate` / `worker` 已启动
- `.env` 中的 `GITLAB_BASE_URL` 与 `GITLAB_TOKEN` 已正确配置
- 如果你在宿主机直接运行 `go run ./cmd/manual-trigger ...`，那么 `.env` 里的 `MYSQL_DSN` / `REDIS_ADDR` 也要能指向 compose 暴露出来的本地端口；默认模板已经是可用值

执行：

```bash
go run ./cmd/manual-trigger --project-id <gitlab_project_id> --mr-iid <mr_iid>
```

如果希望命令阻塞到 run 进入终态：

```bash
go run ./cmd/manual-trigger --project-id <gitlab_project_id> --mr-iid <mr_iid> --wait
```

如果希望输出结构化 JSON：

```bash
go run ./cmd/manual-trigger --project-id <gitlab_project_id> --mr-iid <mr_iid> --json
go run ./cmd/manual-trigger --project-id <gitlab_project_id> --mr-iid <mr_iid> --wait --json
```

示例：

```bash
go run ./cmd/manual-trigger --project-id 123 --mr-iid 45
go run ./cmd/manual-trigger --project-id 123 --mr-iid 45 --wait --wait-timeout 10m
go run ./cmd/manual-trigger --project-id 123 --mr-iid 45 --wait --wait-timeout 10m --poll-interval 2s --json
```

## 一键验证真实 MR

如果你已经有 `.env`，并且只想验证某一条真实 GitLab MR，不想手工查 `project_id`，推荐直接用：

```bash
bash scripts/review-mr.sh "https://github.91jinrong.com/group/repo/-/merge_requests/123"
```

这条脚本会自动：

- 启动 `mysql` / `redis` / `migrate` / `worker`
- 从 MR 链接解析 `group/repo` 和 `mr_iid`
- 调 GitLab API 查询 `project_id`
- 在 compose 网络内执行 `cmd/manual-trigger --wait --json`

它不是在宿主机直接调用数据库，而是在临时 Go 容器里跑 `manual-trigger`，因此可以稳定使用 compose 内的：

- `mysql:3306`
- `redis:6379`

这能避免宿主机本地已经存在其他 MySQL 实例时，把 `127.0.0.1:3306` 误连到错误数据库。

## 现场审计与原始数据

为了方便排查模型兼容性、解析失败、写回异常等问题，`worker` 会把 provider 调用现场完整落到 `audit_logs.detail`：

- `provider_called`
  - 保存完整 provider request
  - 保存完整 provider response
- `provider_failed`
  - 保存完整 provider request
  - 如果是解析失败，也会保存完整原始 response 文本

推荐直接用脚本查看某次 run 的完整现场：

```bash
bash scripts/show-run-audit.sh --latest
bash scripts/show-run-audit.sh 9
```

这会输出：

- `review_runs` 当前状态、错误码、`scope_json`
- `audit_logs` 里该 run 的完整 `detail` JSON

如果你只想临时查数据库，也可以直接执行：

```bash
docker exec -i mreviewer-mysql mysql --default-character-set=utf8mb4 -umreviewer -pmreviewer_password mreviewer -e \
"SELECT id, action, JSON_PRETTY(detail) AS detail FROM audit_logs WHERE entity_type='review_run' AND entity_id=9 ORDER BY id;"
```

脚本退出后：

- 如果返回 `{"ok":true,...}`，说明这条 MR review run 已完成
- 如果模型产出 findings，会写回 GitLab discussion
- 如果没有 findings，也会写一条中文 summary note

命令会：

- 通过 GitLab API 读取当前 MR 元数据
- 自动 upsert 本地 `gitlab_instances` / `projects` / `merge_requests`
- 创建一条 `trigger_type=manual`、`status=pending` 的 `review_runs`

后续由现有 `worker` 自动 claim 并处理这条 run。

也就是说，默认情况下你不需要先手工往数据库插入项目、MR 或策略配置。第一条 webhook 或第一条 manual trigger 就会自动把必要记录写进数据库。

说明：

- 当前主分支的生产 `worker` 会完成 GitLab 拉取、上下文组装、模型调用、finding 落库、gate 发布、GitLab discussion/note 回写
- 如果 `--wait` 结束后状态为 `completed`，通常可以同时在 GitLab MR 页面看到 inline discussion 和 summary note

可选参数：

- `--wait`：阻塞直到 run 进入终态
- `--wait-timeout <duration>`：等待超时，默认 `15m`
- `--poll-interval <duration>`：等待时轮询间隔，默认 `1s`
- `--json`：输出结构化 JSON；失败场景同样返回 JSON

退出码：

- `0`：创建成功；如果使用 `--wait`，则表示最终状态为 `completed` 或 `parser_error`
- `1`：创建失败，或等待后最终状态为 `failed` / `cancelled`，或等待超时
- `2`：命令参数错误

建议同时观察：

```bash
docker compose logs -f worker
```

## Docker Compose 交付说明

本仓库提供单机完整 Docker Compose 交付。

适用范围：

- 单机部署
- 测试环境
- 小规模生产试运行

不包含：

- 多副本高可用
- 外部托管 MySQL / Redis 编排
- Kubernetes 方案

## 常用操作

### 启动

```bash
docker compose up -d --build
```

### 停止

```bash
docker compose down
```

### 查看日志

```bash
docker compose logs -f ingress
docker compose logs -f worker
docker compose logs migrate
docker compose logs -f mysql
docker compose logs -f redis
```

### 查看数据库里的运行结果

```bash
docker exec -it mreviewer-mysql mysql -umreviewer -pmreviewer_password mreviewer -e "show tables;"
docker exec -it mreviewer-mysql mysql -umreviewer -pmreviewer_password mreviewer -e "select id,trigger_type,status,created_at from review_runs order by id desc limit 10;"
docker exec -it mreviewer-mysql mysql -umreviewer -pmreviewer_password mreviewer -e "select id,severity,title,path from review_findings order by id desc limit 20;"
```

### 重新构建

```bash
docker compose up -d --build --force-recreate
```

## 调试与故障演练

### 调试方法

- 检查 `ingress` 是否收到 webhook
- 检查 `worker` 是否正常运行
- 检查 MySQL / Redis 连接
- 检查 GitLab token / webhook secret / LLM API key 是否正确
- 检查 `.env` 是否与实际部署环境一致

### 日志位置

主要通过容器日志查看：

```bash
docker compose logs -f ingress
docker compose logs -f worker
```

### 故障演练建议

- 临时停掉 MySQL，验证请求失败时不会留下脏数据
- 恢复 MySQL 后重试 webhook / note command，确认无重复副作用
- 模拟 Redis 不可用，确认系统以降级模式继续运行

## 安全建议

- 生产环境不要使用默认示例密钥
- GitLab token 只给最小必要权限
- webhook secret 必须启用
- 模型 API key 仅放在部署环境变量中，不要提交到仓库
- 建议用反向代理 / TLS 暴露 `ingress`

## 验证命令

```bash
go test -p 5 ./...
go test -run '^$' ./...
./.factory/bin/golangci-lint run --timeout 5m
```

## GitHub 上传

如果需要推送到 GitHub：

```bash
git remote add origin https://github.com/fakechris/mreviewer.git
git branch -M main
git push -u origin main
```
