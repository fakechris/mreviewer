# mreviewer

[![Docker Hub](https://img.shields.io/badge/docker-fakechris%2Fmreviewer-blue)](https://hub.docker.com/u/fakechris)

[中文文档](./README.zh-CN.md) | English

AI-powered Code Review for GitLab Merge Requests. Self-hosted, multi-model support, SQLite or MySQL.

## Features

- 🤖 **Multi-Model Support**: MiniMax, OpenAI, Anthropic, DeepSeek, and more
- 🗄️ **Flexible Storage**: SQLite (single-machine) or MySQL (production)
- 🌐 **GitLab Native**: Webhook integration, discussion comments, CI gate
- 🔄 **Deduplication**: Fingerprint-based + LLM semantic matching
- 📊 **Observability**: Grafana dashboards, audit logs, metrics

## Quick Start

### Two Ways to Deploy

**Method 1: No Git Required** (Recommended for beginners)
- Download 2 files: `docker-compose.prod.yaml` + `.env`
- Edit `.env` with your credentials
- Run: `docker-compose -f docker-compose.prod.yaml up -d`

**Method 2: Full Clone** (For developers)
- Clone repo, configure `.env`, run `docker-compose up -d`

👉 **Detailed guide**: [QUICKSTART.en.md](./QUICKSTART.en.md) | [快速开始指南](./QUICKSTART.zh-CN.md)

### Prerequisites

- Docker & Docker Compose
- GitLab instance with API access
- LLM provider API key (MiniMax, OpenAI, etc.)

### Minimal Example (Method 1)

1. Download files:
   - [docker-compose.prod.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml)
   - [.env template](https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example) (rename to `.env`)

2. Edit `.env`:
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
MINIMAX_API_KEY=your_minimax_key
```

3. Start:
```bash
docker-compose -f docker-compose.prod.yaml up -d
```

Images auto-pulled from Docker Hub:
- `fakechris/mreviewer-worker:main`
- `fakechris/mreviewer-ingress:main`

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
