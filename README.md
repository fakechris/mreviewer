# mreviewer

[中文文档](./README.zh-CN.md) | English

AI-powered Code Review for GitLab Merge Requests. Self-hosted, multi-model support, SQLite or MySQL.

## Features

- 🤖 **Multi-Model Support**: MiniMax, OpenAI, Anthropic, DeepSeek, and more
- 🗄️ **Flexible Storage**: SQLite (single-machine) or MySQL (production)
- 🌐 **GitLab Native**: Webhook integration, discussion comments, CI gate
- 🔄 **Deduplication**: Fingerprint-based + LLM semantic matching
- 📊 **Observability**: Grafana dashboards, audit logs, metrics

## Quick Start

### Prerequisites

- Docker & Docker Compose
- GitLab instance with API access
- LLM provider API key (MiniMax, OpenAI, etc.)

### 1. Clone and Configure

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
```

Edit `.env` with your credentials:

```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
MINIMAX_API_KEY=your_minimax_key
```

### 2. Start Services

```bash
docker-compose up -d
```

### 3. Configure GitLab Webhook

In your GitLab project:
- Settings → Webhooks
- URL: `http://your-server:3100/webhook`
- Secret: (value from `GITLAB_WEBHOOK_SECRET` in `.env`)
- Trigger: Merge request events

## Architecture

```
GitLab → ingress (webhook) → MySQL/SQLite
                           ↓
                        worker → LLM Provider
                           ↓
                    GitLab (discussions)
```

## Documentation

- [Quick Start Guide](./QUICKSTART.md)
- [GitLab Webhook Setup](./WEBHOOK.md)
- [Docker Deployment](./DEPLOYMENT.md)
- [Configuration File](./config.yaml)

## Roadmap

See [TODOS.md](./TODOS.md) for current priorities and [Strategic Direction](./docs/designs/strategic-direction.md) for long-term vision.

## License

MIT License - see [LICENSE](./LICENSE)
