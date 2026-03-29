# 核心功能验证指南

## 前提条件

1. 已配置 `.env` 文件（从 `.env.example` 复制）
2. 填入真实的 GitLab 和 MiniMax 凭证
3. Docker 和 Docker Compose 已安装

## 验证 1: GitLab Webhook Review（完整流程）

### 启动服务

```bash
docker-compose up -d
docker-compose logs -f
```

### 配置 GitLab Webhook

在 GitLab 项目设置中：
- URL: `http://your-server:3100/webhook`
- Secret: `.env` 中的 `GITLAB_WEBHOOK_SECRET`
- 触发器: Merge request events

### 测试

创建或更新一个 MR，观察：
- `docker-compose logs worker` 看到处理日志
- GitLab MR 页面出现 AI review 评论

## 验证 2: CLI 手动触发 Review

### 使用 SQLite（无需 Docker）

```bash
# 配置 SQLite DSN
export MYSQL_DSN="sqlite://./mreviewer.db"

# 手动触发指定 MR
go run ./cmd/manual-trigger \
  --project-id=123 \
  --mr-iid=456 \
  --wait
```

### 使用 MySQL（需要 Docker）

```bash
# 启动 MySQL
docker-compose up -d mysql

# 运行迁移
go run ./cmd/migrate up

# 手动触发
go run ./cmd/manual-trigger \
  --project-id=123 \
  --mr-iid=456 \
  --wait
```

## 验证成功标志

- ✅ Worker 日志显示 "review run completed"
- ✅ GitLab MR 出现 discussion 评论
- ✅ 数据库中有 review_runs 和 review_findings 记录

## 故障排查

```bash
# 查看 worker 日志
docker-compose logs worker

# 查看数据库
docker-compose exec mysql mysql -u mreviewer -p mreviewer

# 检查配置
go run ./cmd/manual-trigger --help
```
