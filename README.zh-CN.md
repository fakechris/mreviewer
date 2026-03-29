# mreviewer

中文文档 | [English](./README.md)

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

### 1. 克隆并配置

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
```

编辑 `.env` 填入你的凭证：

```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
MINIMAX_API_KEY=your_minimax_key
```

### 2. 启动服务

使用 Docker Hub 预构建镜像：

```bash
docker-compose up -d
```

镜像将自动拉取：
- `fakechris/mreviewer-worker:main`
- `fakechris/mreviewer-ingress:main`

### 3. 配置 GitLab Webhook

在 GitLab 项目中：
- 设置 → Webhooks
- URL: `http://your-server:3100/webhook`
- Secret: （`.env` 中的 `GITLAB_WEBHOOK_SECRET` 值）
- 触发器: Merge request events

## 架构

```
GitLab → ingress (webhook) → MySQL/SQLite
                           ↓
                        worker → LLM Provider
                           ↓
                    GitLab (discussions)
```

## 文档

- [快速开始](./QUICKSTART.md)
- [GitLab Webhook 配置](./WEBHOOK.md)
- [Docker 部署](./DEPLOYMENT.md)
- [配置文件](./config.yaml)

## Roadmap

查看 [TODOS.md](./TODOS.md) 了解当前优先级，[战略方向](./docs/designs/strategic-direction.md) 了解长期愿景。

## 许可证

MIT License - 详见 [LICENSE](./LICENSE)
