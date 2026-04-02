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

## Choose Your Mode

- **Personal CLI**: one binary, local SQLite, no Docker required. Best for individual developers reviewing GitHub/GitLab PRs on demand.
- **Enterprise Webhook**: `ingress` + `worker` + `/admin/` control plane, MySQL-first, optional Redis, automatic webhook processing and operator actions.

## Personal CLI Quick Start

### 1. Install the binary

Download from GitHub Releases or run the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh | bash
```

Homebrew is also supported from a release-provided formula asset:

```bash
brew install ./mreviewer.rb
```

### 2. Generate a personal config

```bash
mreviewer init --provider openai
```

This writes `config.yaml`, creates `.mreviewer/state/`, and defaults to local SQLite.

### 3. Export your tokens

```bash
export OPENAI_API_KEY=...
export GITHUB_TOKEN=...
# optional:
export GITLAB_BASE_URL=https://gitlab.example.com
export GITLAB_TOKEN=...
```

### 4. Verify the setup

```bash
mreviewer doctor
```

### 5. Run a review

```bash
mreviewer review \
  --target https://github.com/acme/repo/pull/17 \
  --output both \
  --publish artifact-only \
  --reviewer-packs security,architecture,database
```

### 6. Optional: run local webhooks and dashboard

```bash
mreviewer serve
```

By default this starts:
- local SQLite at `.mreviewer/state/mreviewer.db`
- GitLab webhook: `POST /webhook`
- GitHub webhook: `POST /github/webhook`
- local admin page: `/admin/`

Use `mreviewer serve --db file:/custom/path.db` to override the local database path.

## Personal CLI Commands

- `mreviewer init`: generate a personal config template
- `mreviewer doctor`: validate config, database, provider routes, and platform tokens
- `mreviewer review`: review one GitHub/GitLab target
- `mreviewer serve`: run ingress + worker in one local process with embedded migrations

Backward compatibility remains: `mreviewer --target ...` still works and maps to `review`.

## CLI Review Output

Key flags:

- `--target`: GitHub PR or GitLab MR URL
- `--targets`: comma-separated GitHub/GitLab PR or MR URLs for multi-target review and aggregate comparison
- `--output`: `markdown`, `json`, or `both`
- `--publish`: `full-review-comments`, `summary-only`, or `artifact-only`
- `--reviewer-packs`: comma-separated reviewer packs
- `--route`: provider route override
- `--advisor-route`: optional stronger second-opinion provider route
- `--exit-mode`: `never` or `requested_changes`; returns exit code `3` when the final verdict requests changes
- `--compare-live`: comma-separated reviewer IDs/kinds already present on the target PR/MR, for example `reviewer-a,reviewer-b`
- `--compare-artifacts`: comma-separated JSON artifact paths to compare against the current review bundle

JSON output includes:
- `review_brief`
- `judge_verdict`
- `decision_benchmark`
- `comparison`
- `aggregate_comparison`
- `advisor_artifact`

Markdown output starts with `# Review Decision Brief` and surfaces:
- `Final Verdict`
- `What To Fix First`
- `Specialist Signals`
- `Reviewer Overlap` when comparison is enabled

Runtime environment overrides:

- `REVIEW_PACKS`: default reviewer packs for CLI/runtime processing
- `REVIEW_ADVISOR_ROUTE`: default stronger second-opinion route
- `REVIEW_COMPARE_REVIEWERS`: comma-separated external reviewer IDs to compare during runtime processing

## Enterprise Webhook Deployment

Use the enterprise path when you need:
- automatic GitLab/GitHub webhook reviews
- MySQL-backed queueing and history
- separate `ingress` and `worker` services
- operator actions and dashboard visibility

### Enterprise quick start

```bash
cp .env.example .env
docker compose up -d --build
```

For image-based deployment:

```bash
docker compose -f docker-compose.prod.yaml up -d
```

For custom routes or mounted config:

```bash
docker compose -f docker-compose.prod.yaml -f docker-compose.prod.config.yaml up -d
```

`docker-compose.yaml` is for local developer source builds. `docker-compose.prod.yaml` is for enterprise-style deployment. MySQL is the recommended enterprise default; SQLite remains fine for local single-machine usage.

### Configure GitLab Webhook

1. Go to GitLab project → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
   - For local-network testing, replace `your-server` with your machine's LAN IP, for example `http://10.0.0.16:3100/webhook`
   - Do not use `localhost` unless GitLab and mreviewer are running on the same host
3. Secret: value from `GITLAB_WEBHOOK_SECRET`
4. Trigger: check `Merge request events`
5. Click `Add webhook`

If GitLab rejects the hook with `Invalid url given`, ask a GitLab admin to enable `Allow requests to the local network from web hooks and services`, or use a public HTTPS tunnel instead of a LAN IP.

GitHub webhook path is `POST /github/webhook` and follows the same ingress/worker control plane.

📖 Detailed setup: [WEBHOOK.md](./WEBHOOK.md)

## Admin Control Plane

Enterprise and local `serve` mode both expose `/admin/`.

Key API routes:

- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`
- `/admin/api/runs`
- `/admin/api/trends`
- `/admin/api/ownership`
- `/admin/api/identities`

Set `MREVIEWER_ADMIN_TOKEN` or `ADMIN_TOKEN` to require `Authorization: Bearer <token>` on the admin routes.

## Manual Trigger (Enterprise Optional)

Trigger a GitLab review without waiting for webhook delivery:

```bash
docker compose exec worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait
```

The worker image also includes `/app/mreviewer` for direct CLI review.

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
