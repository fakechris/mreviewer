# mreviewer

GitLab Merge Request 自动 Code Review 服务，面向单机 Docker Compose 部署。

## 组件

- `ingress`: 接收 GitLab webhook，请求入口，默认端口 `3100`
- `worker`: 拉取 MR 上下文、调用模型、写回评论/结果
- `mysql`: MySQL 8.4，业务主库
- `redis`: Redis 7，限流/协调降级依赖

## 生产拓扑

单机部署：

- GitLab -> `ingress` `/webhook`
- `ingress` -> MySQL
- `worker` -> MySQL / Redis / GitLab API / LLM Provider
- `worker` -> GitLab 写回评论与状态

## 环境变量

复制配置模板：

```bash
cp .env.example .env
```

关键变量：

- `APP_ENV`: 建议生产设为 `production`
- `PORT`: ingress 监听端口，默认 `3100`
- `MYSQL_DSN`: MySQL 连接串
- `REDIS_ADDR`: Redis 地址
- `GITLAB_BASE_URL`: 你的 GitLab 地址，例如 `https://gitlab.example.com`
- `GITLAB_TOKEN`: GitLab API Token
- `GITLAB_WEBHOOK_SECRET`: GitLab webhook secret
- `ANTHROPIC_BASE_URL`: 模型供应商 Anthropic-compatible 地址
- `ANTHROPIC_API_KEY`: 模型 API Key
- `ANTHROPIC_MODEL`: 模型名

## GitLab 配置

1. 在 GitLab 创建可读取 MR / diff / note，并可写评论的 token
2. 在目标项目中配置 webhook：
   - URL: `http(s)://<your-host>:3100/webhook`
   - Secret: 与 `GITLAB_WEBHOOK_SECRET` 一致
   - 事件：Merge Request events、Note events
3. 确保 GitLab 可以访问部署主机的 `3100` 端口

## 模型供应商配置

当前实现使用 Anthropic-compatible 接口。

最少需要：

- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL`

示例已在 `.env.example` 中给出，默认是 MiniMax Anthropic-compatible 形式。

## 安装与初始化

### 1. 准备配置

```bash
cp .env.example .env
```

填写 `.env` 中的 GitLab 与模型供应商配置。

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

### 3. 健康检查

```bash
curl -i http://127.0.0.1:3100/health
docker compose ps
```

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

6. 观察 `worker` 处理、拉取上下文、调用模型、写回评论：

```bash
docker compose logs -f worker
```

7. 在 GitLab MR 页面确认：
   - review comment 已写回
   - note command（如 rerun / ignore / resolve / focus）可生效
   - 大 MR 降级 summary / provider route / beta 行为正常

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
