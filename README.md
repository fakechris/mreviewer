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

### Prerequisites

- Docker & Docker Compose
- GitLab instance with API access
- LLM provider API key (MiniMax, OpenAI, etc.)

### Method 1: Minimal Setup (No Git Required)

**For beginners** - Download 2 files and run

1. **Download files**:
   - [docker-compose.prod.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml)
   - [.env template](https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example) (rename to `.env`)

2. **Edit `.env`**:
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
MINIMAX_API_KEY=your_minimax_key
MINIMAX_GROUP_ID=your_group_id
```

3. **Start services**:
```bash
docker-compose -f docker-compose.prod.yaml up -d
```

4. **Verify**:
```bash
docker-compose -f docker-compose.prod.yaml logs -f worker
```

### Method 2: Full Clone (For Developers)

**For developers** - Clone repo and customize

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
# Edit .env with your credentials
docker-compose up -d
```

### Configure GitLab Webhook

**Required for automatic review**

1. Go to GitLab project → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
3. Secret: Value from `GITLAB_WEBHOOK_SECRET` in `.env`
4. Trigger: Check "Merge request events"
5. Click "Add webhook"

📖 Detailed webhook setup: [WEBHOOK.md](./WEBHOOK.md)

### Manual Trigger (Optional)

Trigger review without webhook:

```bash
docker exec -it mreviewer-worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait
```

**Note**: Requires `GITLAB_BASE_URL`, `GITLAB_TOKEN`, and LLM API keys configured in `.env`

## Architecture

```
GitLab → ingress (webhook) → MySQL/SQLite
                           ↓
                        worker → LLM Provider
                           ↓
                    GitLab (discussions)
```

## Documentation

- [GitLab Webhook Setup](./WEBHOOK.md) - Three configuration methods (project/group/system)
- [Docker Deployment](./DEPLOYMENT.md) - Build and deploy to production
- [Contributing Guide](./CONTRIBUTING.md) - How to contribute
- [Configuration](./config.yaml) - Full configuration reference

## Roadmap

See [TODOS.md](./TODOS.md) for current priorities.

## License

MIT License - see [LICENSE](./LICENSE)
