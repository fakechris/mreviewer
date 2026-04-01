# mreviewer

[![Docker Hub](https://img.shields.io/badge/docker-fakechris%2Fmreviewer-blue)](https://hub.docker.com/u/fakechris)

[中文文档](./README.zh-CN.md) | English

AI-powered Code Review for GitLab Merge Requests. Self-hosted, multi-model support, SQLite or MySQL.

## Features

- 🤖 **Multi-Model Support**: MiniMax, OpenAI, Anthropic, DeepSeek, and more
- 🗄️ **Flexible Storage**: SQLite (single-machine) or MySQL (production)
- 🌐 **GitLab Native**: Webhook integration, discussion comments, CI gate
- 🧰 **Portable Review Council CLI**: Review GitHub/GitLab PRs from a single CLI entrypoint
- 🔄 **Deduplication**: Fingerprint-based + LLM semantic matching
- 📊 **Observability**: Grafana dashboards, audit logs, metrics

## Deployment Decision

- Personal / small-team trial: quick start with one provider, webhook, and the built-in `/admin/` control plane
- Enterprise default: MySQL-backed deployment, optional Redis coordination, webhook automation, and the `/admin/` page for queue/concurrency/failure visibility
- SQLite remains supported for single-machine setups, but MySQL is the default recommendation for shared or production environments

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

2. **Edit `.env` with one of these equal quick-start profiles**:

#### Method 1A: MiniMax
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=minimax
LLM_API_KEY=your_minimax_key
LLM_BASE_URL=https://api.minimaxi.com/anthropic
LLM_MODEL=MiniMax-M2.7-highspeed
```

#### Method 1B: Anthropic
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=anthropic
LLM_API_KEY=your_anthropic_key
LLM_BASE_URL=https://api.anthropic.com
LLM_MODEL=claude-sonnet-4-6
```

#### Method 1C: ChatGPT / OpenAI
```bash
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token
GITLAB_WEBHOOK_SECRET=your_webhook_secret
LLM_PROVIDER=openai
LLM_API_KEY=your_openai_key
LLM_BASE_URL=https://api.openai.com/v1
LLM_MODEL=gpt-5.4
```

All three quick-start profiles use the same env-only contract and the same start command.

3. **Start services**:
```bash
docker compose -f docker-compose.prod.yaml up -d
```

4. **Verify**:
```bash
docker compose -f docker-compose.prod.yaml logs -f worker
```

### Method 2: Advanced No-Git Setup (Multi-Provider / OpenAI / Custom Routes)

**For operators** - Download 4 files and mount a custom config

1. **Download files**:
   - [docker-compose.prod.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml)
   - [docker-compose.prod.config.yaml](https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.config.yaml)
   - [.env template](https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example) (rename to `.env`)
   - [config example](https://raw.githubusercontent.com/fakechris/mreviewer/main/config.example.yaml) (rename to `config.yaml`)

2. **Edit `.env` and `config.yaml`**:
- `docker-compose.prod.yaml` passes through your entire `.env`, so custom provider secrets are available inside the containers.
- `docker-compose.prod.config.yaml` mounts your local `config.yaml` into `/app/config.yaml`.
- `config.example.yaml` supports `${VAR}` syntax; environment variables are expanded at startup.

3. **Start services**:
```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml up -d
```

Use this path for DeepSeek, Fireworks, mixed-provider routing, or SQLite deployments. See [config.example.yaml](./config.example.yaml) for a working template.

4. **Verify**:
```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml logs -f worker
```

### Method 3: Full Clone (For Developers)

**For developers** - Clone repo and run local source builds

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
cp .env.example .env
# Edit .env with your credentials
docker compose up -d --build
```

This path builds `ingress` and `worker` from your local checkout, so code changes are reflected in the running containers.

### Portable Review Council CLI

The new `mreviewer` CLI runs the portable review council path directly against a GitHub or GitLab PR URL.

Run from source:

```bash
go run ./cmd/mreviewer \
  --target https://github.com/acme/repo/pull/17 \
  --output both \
  --publish full-review-comments \
  --reviewer-packs security,architecture,database
```

Run inside the worker image:

```bash
docker compose exec worker /app/mreviewer \
  --target https://github.com/acme/repo/pull/17 \
  --output json \
  --publish artifact-only
```

Key flags:

- `--target`: GitHub PR or GitLab MR URL
- `--targets`: comma-separated GitHub/GitLab PR or MR URLs for multi-target review and aggregate comparison
- `--output`: `markdown`, `json`, or `both`
- `--publish`: `full-review-comments`, `summary-only`, or `artifact-only`
- `--reviewer-packs`: comma-separated reviewer packs
- `--route`: provider route override
- `--advisor-route`: optional stronger second-opinion provider route
- `--exit-mode`: `never` or `requested_changes`; returns exit code `3` when the final verdict requests changes
- `--compare-live`: comma-separated reviewer IDs/kinds already present on the target PR/MR, for example `codex,coderabbit`
- `--compare-artifacts`: comma-separated JSON artifact paths to compare against the current review bundle

The CLI emits the review bundle in JSON mode and, when comparison flags are provided, includes a comparison report with agreement rate, shared findings, and reviewer-unique findings.
When `--targets` is provided, the JSON output also includes `aggregate_comparison` so you can compare reviewer agreement across multiple GitHub/GitLab changes in one run.
When advisor or benchmark output is present, the JSON payload also includes `advisor_artifact`, `judge_verdict`, and `decision_benchmark`.
For GitHub targets, the CLI also updates the commit status context `mreviewer/ai-review` while the review is running and after it completes.

Runtime environment overrides:

- `REVIEW_PACKS`: default reviewer packs for worker/runtime processing
- `REVIEW_ADVISOR_ROUTE`: default stronger second-opinion route for CLI/runtime processing
- `REVIEW_COMPARE_REVIEWERS`: comma-separated external reviewer IDs to compare during runtime processing

### Configure GitLab Webhook

**Required for automatic review**

1. Go to GitLab project → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
   - For local-network testing, replace `your-server` with your machine's LAN IP, for example `http://10.0.0.16:3100/webhook`
   - Do not use `localhost` unless GitLab and mreviewer are running on the same host
3. Secret: Value from `GITLAB_WEBHOOK_SECRET` in `.env`
4. Trigger: Check "Merge request events"
5. Click "Add webhook"

If your GitLab instance rejects the hook with `Invalid url given`, ask a GitLab admin to enable `Allow requests to the local network from web hooks and services`, or use a public HTTPS tunnel instead of a LAN IP.

📖 Detailed webhook setup: [WEBHOOK.md](./WEBHOOK.md)

### Admin Control Plane

`ingress` now serves a small read-only operator page at `/admin/` plus JSON endpoints at:

- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`

Set `MREVIEWER_ADMIN_TOKEN` or `ADMIN_TOKEN` to require `Authorization: Bearer <token>` on those routes. This is the fastest way to inspect queue depth, active workers, and recent failures without tailing logs.

### Manual Trigger (Optional)

Trigger review without webhook:

```bash
docker compose exec worker /app/manual-trigger \
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
- [Enterprise Webhook Architecture](./docs/architecture/enterprise-webhook.md) - Queue semantics, concurrency model, and control-plane design
- [Admin Dashboard Operations](./docs/operations/admin-dashboard.md) - `/admin/` usage and bearer auth
- [Failure Playbook](./docs/operations/failure-playbook.md) - How to triage provider, worker, and supersede failures
- [Contributing Guide](./CONTRIBUTING.md) - How to contribute
- [Configuration](./config.yaml) - Safe default runtime config
- [Advanced Configuration Template](./config.example.yaml) - Multi-provider example with env expansion

## Roadmap

See [TODOS.md](./TODOS.md) for current priorities.

## License

MIT License - see [LICENSE](./LICENSE)
