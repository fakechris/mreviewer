# mreviewer

[![Docker Hub](https://img.shields.io/badge/docker-fakechris%2Fmreviewer-blue)](https://hub.docker.com/u/fakechris)

[中文文档](./README.zh-CN.md) | English

GitLab Merge Request AI 代码审查工具。支持自托管、多模型、SQLite/MySQL。

## 特性

- 🤖 **多模型支持**: MiniMax、OpenAI、Anthropic、DeepSeek 等
- 🗄️ **灵活存储**: SQLite（单机）或 MySQL（生产）
- 🌐 **GitLab 原生**: Webhook 集成、讨论评论、CI 门禁
- 🔄 **智能去重**: 指纹匹配 + LLM 语义去重
- 📊 **可观测性**: Grafana 仪表板、审计日志、指标

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

2. **编辑 `.env`**：
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
MINIMAX_API_KEY=your_minimax_key
MINIMAX_GROUP_ID=your_group_id
```

3. **启动服务**：
```bash
docker-compose -f docker-compose.prod.yaml up -d
```

4. **验证**：
```bash
docker-compose -f docker-compose.prod.yaml logs -f worker
```

### 方式 2：完整克隆（开发者）

**适合开发者** - 克隆仓库并自定义

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
# 编辑 .env 填入凭证
docker-compose up -d
```

### 配置 GitLab Webhook

**自动审查必需**

1. 进入 GitLab 项目 → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
3. Secret: 填入 `.env` 中的 `GITLAB_WEBHOOK_SECRET`
4. 触发器: 勾选 "Merge request events"
5. 点击 "Add webhook"

📖 详细配置: [WEBHOOK.md](./WEBHOOK.md)

### 手动触发（可选）

无需 webhook 手动触发审查：

```bash
docker exec -it mreviewer-worker /app/manual-trigger \
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
- [贡献指南](./CONTRIBUTING.md) - 如何贡献
- [配置参考](./config.yaml) - 完整配置说明

## Roadmap

查看 [TODOS.md](./TODOS.md) 了解当前优先级。

## 许可证

MIT License - 详见 [LICENSE](./LICENSE)
