# Docker 部署指南

## 企业默认部署 / enterprise deployment

企业部署默认推荐：

- `MySQL` 作为中心控制面数据库
- `Redis` 作为可选协调层
- `ingress` + `worker` 分离部署
- 配置 `MREVIEWER_ADMIN_TOKEN` 或 `ADMIN_TOKEN`，通过 `/admin/` 查看排队、并发和失败

`SQLite` 继续支持单机部署，但不作为企业默认。

## 一键启动（本地测试）

```bash
# 1. 配置环境变量
cp .env.example .env
# 编辑 .env，填入 GitLab 凭证和一组 quick-start provider 凭证
# 默认示例使用:
#   LLM_PROVIDER=minimax
#   LLM_API_KEY=...
#   LLM_BASE_URL=https://api.minimaxi.com/anthropic
#   LLM_MODEL=MiniMax-M2.7-highspeed

# 2. 启动所有服务
docker compose up -d --build

# 3. 查看日志
docker compose logs -f worker
```

说明：

- `docker-compose.yaml` 面向开发者，本地源码会被重新构建。
- 如果你想用 Docker Hub 镜像做服务器部署，使用下方的 `docker-compose.prod.yaml`。
- `MiniMax`、`Anthropic`、`ChatGPT/OpenAI` 都可以直接走同一套 `LLM_PROVIDER / LLM_API_KEY / LLM_BASE_URL / LLM_MODEL` quick-start 合约。
- 如果你需要 DeepSeek、Fireworks、多 provider 路由或 SQLite，自定义 `config.yaml` 并配合 `docker-compose.prod.config.yaml` 一起使用。
- Compose 文件不再固定 `container_name`，便于同机并行测试多个环境。进入 worker 时优先使用 `docker compose exec worker ...`。

## 企业运维最小配置

至少补齐以下环境变量：

```bash
DATABASE_DSN=mysql://...
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=...
GITLAB_WEBHOOK_SECRET=...
MREVIEWER_ADMIN_TOKEN=replace-with-strong-token
```

Admin 页面与 API：

- `http://your-server:3100/admin/`
- `http://your-server:3100/admin/api/queue`
- `http://your-server:3100/admin/api/concurrency`
- `http://your-server:3100/admin/api/failures`

访问这些路由时带上：

```bash
Authorization: Bearer <MREVIEWER_ADMIN_TOKEN>
```

## 构建和推送到私有仓库

### 1. 构建镜像

```bash
# 构建 worker
docker build --build-arg TARGET_CMD=./cmd/worker -t your-registry/mreviewer-worker:latest .

# 构建 ingress
docker build --build-arg TARGET_CMD=./cmd/ingress -t your-registry/mreviewer-ingress:latest .
```

### 2. 推送到仓库

```bash
docker push your-registry/mreviewer-worker:latest
docker push your-registry/mreviewer-ingress:latest
```

### 3. 服务器部署

在服务器上创建 `docker-compose.prod.yaml`:

```yaml
services:
  mysql:
    image: mysql:8.4
    restart: unless-stopped
    environment:
      MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD}
      MYSQL_DATABASE: mreviewer
      MYSQL_USER: mreviewer
      MYSQL_PASSWORD: ${MYSQL_PASSWORD}
    volumes:
      - mysql_data:/var/lib/mysql

  redis:
    image: redis:7
    restart: unless-stopped

  ingress:
    image: your-registry/mreviewer-ingress:latest
    restart: unless-stopped
    ports:
      - "3100:3100"
    env_file: .env
    depends_on:
      - mysql
      - redis

  worker:
    image: your-registry/mreviewer-worker:latest
    restart: unless-stopped
    env_file: .env
    depends_on:
      - mysql
      - redis

volumes:
  mysql_data:
```

启动：

```bash
docker compose -f docker-compose.prod.yaml up -d
```

如果你需要挂载自定义 `config.yaml`，改为：

```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml up -d
```

## 企业运行语义

- Webhook 入口保持薄入口：验签、去重、落库、入队、快速返回
- Review 队列采用 `latest-head-wins` 语义
- 同一个 MR 出现新 head 时，旧的 pending run 会被 supersede；旧的 running run 会被标记取消并在后续写点停止产出
- Grafana 适合看时序与趋势，`/admin/` 适合看实时 control-plane 状态
